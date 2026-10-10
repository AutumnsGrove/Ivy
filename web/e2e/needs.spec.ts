import { expect, test } from './api';
import { toast } from './helpers';

// 5c.4: the Needs attention view, the inline reason and the dismiss, on phone and desktop.

test.describe('needs attention', () => {
	test('lists the flagged mail with its reason, and nothing else', async ({ page }) => {
		await page.goto('/needs');
		await expect(page.getByRole('heading', { name: 'Needs attention' })).toBeVisible();
		await expect(page.getByText('2 need you')).toBeVisible();
		await expect(page.getByText('Mara asks whether you can move her old posts over.')).toBeVisible();
		await expect(page.getByText('A rights holder says you have a deadline to respond.')).toBeVisible();
		await expect(page.getByText('Wildflower Weekly')).toHaveCount(0);
	});

	test('dismiss clears the row and the inbox count', async ({ page }) => {
		await page.goto('/needs');
		await page.getByRole('button', { name: /^Dismiss: Moving my blog/ }).click();
		await expect(toast(page, 'Cleared from Needs attention')).toBeVisible();
		await expect(page.getByText('Mara asks whether')).toHaveCount(0);
		await expect(page.getByText('1 need you')).toBeVisible();

		await page.goto('/');
		await expect(page.getByText('1 need you · 2 unread')).toBeVisible();
	});

	test('says so when nothing needs you', async ({ page }) => {
		await page.goto('/needs');
		for (const name of [/^Dismiss: Moving my blog/, /^Dismiss: Notice of alleged/]) {
			await page.getByRole('button', { name }).click();
		}
		await expect(page.getByText('Nothing needs you right now.')).toBeVisible();
	});

	test('the inbox shows the reason on the row and links to the view', async ({ page }) => {
		await page.goto('/');
		await expect(page.getByText('Mara asks whether you can move her old posts over.').first()).toBeVisible();
		const link = page.getByRole('link', { name: /Needs attention/ });
		if (!(await link.isVisible())) await page.getByRole('button', { name: 'All inboxes' }).click();
		await link.click();
		await expect(page).toHaveURL(/\/needs$/);
	});

	test('the reader shows the reason and dismisses it', async ({ page }) => {
		await page.goto('/m/m1');
		const read = page.getByRole('article');
		await expect(read.getByText('Mara asks whether you can move her old posts over.')).toBeVisible();
		await read.getByRole('button', { name: 'Dismiss' }).click();
		await expect(read.getByText('needs you')).toHaveCount(0);
		await expect(read.getByRole('button', { name: 'Dismiss' })).toHaveCount(0);
	});

	test('the odds sheet shows the second look, and that it was cleared', async ({ page }) => {
		await page.goto('/m/m1');
		await page.getByRole('button', { name: 'More', exact: true }).click();
		await page.getByRole('button', { name: 'Show the odds' }).click();
		const second = page.getByRole('dialog', { name: 'The odds' }).getByRole('group', { name: 'Second look' });
		await expect(second).toContainText('Needs you');
		await expect(second).toContainText('Mara asks whether you can move her old posts over.');
		await expect(second).not.toContainText('Cleared');
	});
});
