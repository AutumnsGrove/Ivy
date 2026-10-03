import { readFile } from 'node:fs/promises';
import { expect, test } from './api';

const total = (page: import('@playwright/test').Page) => page.locator('.total');
const cents = async (page: import('@playwright/test').Page) => Number((await total(page).innerText()).replace('$', ''));

test.describe('spend and calls', () => {
	test('settings opens it, and a longer period never shows less than a shorter one', async ({ page }) => {
		await page.goto('/settings');
		await page.getByRole('link', { name: 'Spend and calls' }).click();
		await expect(page).toHaveURL(/\/settings\/spend$/);
		await expect(page.getByText('Spent in the last 7 days')).toBeVisible();

		const week = await cents(page);
		await page.getByRole('link', { name: 'All time' }).click();
		await expect(page.getByText('Spent so far')).toBeVisible();
		expect(await cents(page)).toBeGreaterThanOrEqual(week);

		await page.getByRole('link', { name: 'Today' }).click();
		await expect(page.getByText('Spent today')).toBeVisible();
		expect(await cents(page)).toBeLessThanOrEqual(week);
	});

	test('an account with smart features off is listed as sending nothing', async ({ page }) => {
		await page.goto('/settings/spend');
		await expect(page.getByText('Smart features are off. Nothing is sent.').first()).toBeVisible();
	});

	test('nothing sent: says why, instead of showing an empty dashboard', async ({ page }) => {
		await page.goto('/settings/spend?scenario=no-spend');
		await expect(total(page)).toHaveText('$0.00');
		await expect(page.getByText('Smart features are off for every account, so nothing has been sent.')).toBeVisible();
		await expect(page.getByRole('group', { name: 'By feature' })).toHaveCount(0);
	});

	test('cap reached: the month is full and the page says Ivy has paused', async ({ page }) => {
		await page.goto('/settings/spend?scenario=cap-hit');
		await expect(page.getByText(/monthly cap is reached/)).toBeVisible();
		await expect(page.getByRole('progressbar', { name: /monthly cap/ })).toHaveAttribute('aria-valuenow', '100');
	});

	test('the scenario survives the click through to the log and back', async ({ page }) => {
		await page.goto('/settings/spend?scenario=no-spend');
		await page.getByRole('link', { name: 'See every call' }).click();
		await expect(page.getByText('No calls to show yet.')).toBeVisible();
		await page.getByRole('link', { name: 'Back' }).click();
		await expect(page).toHaveURL(/scenario=no-spend/);
	});
});

test.describe('call log', () => {
	test('filters to one outcome and puts the URL in step', async ({ page }) => {
		await page.goto('/settings/spend/calls');
		await page.getByRole('button', { name: 'Errors' }).click();
		await expect(page).toHaveURL(/outcome=error/);
		const rows = page.locator('li.call');
		await expect(rows.first()).toBeVisible();
		await expect(rows.first().locator('.dot')).toHaveClass(/error/);
		for (const dot of await rows.locator('.dot').all()) await expect(dot).toHaveClass(/error/);
	});

	test('a bad outcome in the URL is ignored, not trusted', async ({ page }) => {
		await page.goto('/settings/spend/calls?outcome=__proto__');
		await expect(page.locator('li.call').first()).toBeVisible();
		await expect(page.getByRole('button', { name: 'All' })).toHaveAttribute('aria-pressed', 'true');
	});

	test('shows older calls below the ones already there', async ({ page }) => {
		await page.goto('/settings/spend/calls');
		const rows = page.locator('li.call');
		const first = await rows.count();
		await page.getByRole('button', { name: 'Show older' }).click();
		await expect.poll(() => rows.count()).toBeGreaterThan(first);
	});

	test('a Needs-me call shows the probability vector', async ({ page }) => {
		await page.goto('/settings/spend/calls');
		await expect(page.getByRole('progressbar', { name: /^Needs me \d+ percent$/ }).first()).toBeVisible();
	});

	test('saves what is on screen as a CSV', async ({ page }) => {
		await page.goto('/settings/spend/calls');
		const download = page.waitForEvent('download');
		await page.getByRole('button', { name: 'Save as CSV' }).click();
		const file = await download;
		expect(file.suggestedFilename()).toBe('ivy-calls.csv');
		const text = await readFile((await file.path())!, 'utf8');
		expect(text.startsWith('time,account,feature,model,outcome,reason,cost_usd,tokens,latency_ms\r\n')).toBe(true);
		expect(text.trimEnd().split('\r\n').length).toBeGreaterThan(5);
	});
});
