import type { Page } from '@playwright/test';
import { expect, test } from './api';
import { toast } from './helpers';

// Issue #11: act on several messages at once. A Select button turns the list into
// a chooser; "all" means what is loaded; a move is confirmed once with the count.

const list = (page: Page) => page.getByRole('list').first();
const card = (page: Page, text: string) => list(page).getByRole('checkbox', { name: new RegExp(text) });
const bar = (page: Page) => page.getByRole('toolbar', { name: 'Selection' });

test.describe('selecting several messages', () => {
	test('Select turns the cards into choices and tapping one does not open it', async ({ page }) => {
		await page.goto('/');
		await expect(bar(page)).toHaveCount(0);

		await page.getByRole('button', { name: 'Select', exact: true }).click();
		await expect(bar(page)).toBeVisible();
		await expect(bar(page)).toContainText('0 selected');

		await card(page, 'Moving my blog').click();
		await card(page, 'Notice of alleged').click();
		await expect(bar(page)).toContainText('2 selected');
		await expect(page).toHaveURL(/\/(\?.*)?$/); // still on the list: nothing was opened
		await expect(card(page, 'Moving my blog')).toBeChecked();

		await card(page, 'Moving my blog').click();
		await expect(bar(page)).toContainText('1 selected');
	});

	test('Select all means what is loaded, and Done leaves selection', async ({ page }) => {
		await page.goto('/');
		await page.getByRole('button', { name: 'Select', exact: true }).click();
		await bar(page).getByRole('button', { name: 'Select all' }).click();
		const rows = await list(page).getByRole('checkbox').count();
		expect(rows).toBeGreaterThan(1);
		await expect(bar(page)).toContainText(`${rows} selected`);

		await bar(page).getByRole('button', { name: 'Done' }).click();
		await expect(bar(page)).toHaveCount(0);
		await expect(list(page).getByRole('checkbox')).toHaveCount(0);
	});

	test('archiving asks once with the count, then one toast with one Undo', async ({ page }) => {
		await page.goto('/');
		await page.getByRole('button', { name: 'Select', exact: true }).click();
		await card(page, 'Moving my blog').click();
		await card(page, 'Notice of alleged').click();
		await bar(page).getByRole('button', { name: 'Archive' }).click();

		const dialog = page.getByRole('dialog', { name: 'Archive 2 messages?' });
		await expect(dialog).toBeVisible();
		await dialog.getByRole('button', { name: 'Archive' }).click();

		await expect(toast(page, 'Archived 2')).toBeVisible();
		await expect(toast(page, 'Archived 2').getByRole('button', { name: 'Undo' })).toBeVisible();
		await expect(bar(page)).toHaveCount(0); // done: selection ends
		await expect(page.getByText('Moving my blog').first()).toBeHidden(); // hidden while the move is live
	});

	test('cancelling the confirmation changes nothing and keeps the selection', async ({ page }) => {
		await page.goto('/');
		await page.getByRole('button', { name: 'Select', exact: true }).click();
		await card(page, 'Moving my blog').click();
		await bar(page).getByRole('button', { name: 'Delete' }).click();
		await page.getByRole('dialog', { name: 'Move 1 message to Trash?' }).getByRole('button', { name: 'Cancel' }).click();

		await expect(bar(page)).toContainText('1 selected');
		await expect(toast(page, /Moved/)).toHaveCount(0);
	});

	test('flagging needs no confirmation', async ({ page }) => {
		await page.goto('/');
		await page.getByRole('button', { name: 'Select', exact: true }).click();
		await card(page, 'Moving my blog').click();
		await bar(page).getByRole('button', { name: 'More' }).click();
		await page.getByRole('button', { name: 'Flag', exact: true }).click();
		await expect(toast(page, 'Flagged 1')).toBeVisible();
	});

	test('a full queue refuses the whole batch and says why', async ({ page }) => {
		await page.goto('/?scenario=batch-full');
		await page.getByRole('button', { name: 'Select', exact: true }).click();
		await card(page, 'Moving my blog').click();
		await bar(page).getByRole('button', { name: 'More' }).click();
		await page.getByRole('button', { name: 'Flag', exact: true }).click();

		await expect(toast(page, /too many unsent actions/)).toBeVisible();
		await expect(bar(page)).toContainText('1 selected'); // still chosen, nothing was queued
	});
});
