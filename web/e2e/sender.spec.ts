import type { Page } from '@playwright/test';
import { expect, test } from './api';

// Issue #15: tapping the sender says who they really are. Names and addresses are
// sender-controlled, so the real address always sits beside the name.

// The desktop layout shows the inbox beside the reader, so look inside the reader.
const inReader = (page: Page, name: RegExp) => page.locator('article.msg').getByRole('button', { name });
const sheetOf = (page: Page) => page.getByRole('dialog', { name: 'Sender details' });

test.describe('sender sheet', () => {
	test('opens from the sender, shows the real address and the others on the message', async ({ page }) => {
		await page.goto('/m/m1');
		await inReader(page, /Mara Linden/).click();

		const sheet = sheetOf(page);
		await expect(sheet).toBeVisible();
		await expect(sheet.locator('.addr')).toHaveText('mara@example.com');
		await expect(sheet.getByText('Taro Kimura')).toBeVisible(); // on Cc
		await expect(sheet.getByText('Your mail provider did not check who sent this.')).toBeVisible();

		await sheet.getByRole('link', { name: /Everything from them/ }).click();
		await expect(page).toHaveURL(/\/people\/p-ml$/);
	});

	test('a Cc person opens the same sheet for them', async ({ page }) => {
		await page.goto('/m/m1');
		await inReader(page, /Mara Linden/).click();
		await sheetOf(page).getByRole('button', { name: /Taro Kimura/ }).click();
		await expect(sheetOf(page).locator('.addr')).toHaveText('taro@example.com');
	});

	test('the To line opens the sheet too', async ({ page }) => {
		await page.goto('/m/m1');
		await inReader(page, /^to hello@/).click();
		await expect(sheetOf(page).locator('.addr')).toHaveText('hello@example.com');
	});

	test('closes with Escape', async ({ page }) => {
		await page.goto('/m/m1');
		await inReader(page, /Mara Linden/).click();
		await page.keyboard.press('Escape');
		await expect(sheetOf(page)).toHaveCount(0);
	});

	test('warns when the name contains a different address, and shows the real one', async ({ page }) => {
		await page.goto('/m/m1?scenario=hostile-sender');
		await inReader(page, /security@mybank\.com/).click();
		const sheet = sheetOf(page);
		await expect(sheet.locator('.addr')).toHaveText('phish@evil.test');
		await expect(sheet.getByRole('note')).toContainText('different address');
	});

	test('a very long right-to-left name stays inside the sheet and the page', async ({ page }) => {
		await page.goto('/m/m2?scenario=hostile-sender');
		await inReader(page, /مرحبا/).click();
		const sheet = sheetOf(page);
		const box = (await sheet.boundingBox())!;
		const name = (await sheet.locator('.who').boundingBox())!;
		expect(name.x).toBeGreaterThanOrEqual(box.x - 1);
		expect(name.x + name.width).toBeLessThanOrEqual(box.x + box.width + 1);
		expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
	});
});
