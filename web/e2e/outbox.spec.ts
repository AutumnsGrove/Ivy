import { expect, test } from './api';
import { toast } from './helpers';

// Reader actions now go through the outbox. A move or delete is confirmed first
// (CLAUDE.md rule 6) and the success toast offers the inverse op as Undo.
test.describe('outbox actions', () => {
	test('archiving confirms first, then queues the move and offers undo', async ({ page }) => {
		await page.goto('/m/m1');
		await page.getByRole('button', { name: 'Archive' }).click();

		const dialog = page.getByRole('dialog', { name: 'Archive this message?' });
		await expect(dialog).toBeVisible();
		await dialog.getByRole('button', { name: 'Archive' }).click();

		await expect(toast(page, 'Archived')).toBeVisible();
		await expect(toast(page, 'Archived').getByRole('button', { name: 'Undo' })).toBeVisible();
	});

	test('cancelling the confirmation changes nothing', async ({ page }) => {
		await page.goto('/m/m1');
		await page.getByRole('button', { name: 'Archive' }).click();

		const dialog = page.getByRole('dialog', { name: 'Archive this message?' });
		await dialog.getByRole('button', { name: 'Cancel' }).click();
		await expect(dialog).toHaveCount(0);
		await expect(toast(page, 'Archived')).toHaveCount(0);
	});

	test('deleting confirms with a stronger, dangerous dialog', async ({ page }) => {
		await page.goto('/m/m1');
		await page.getByRole('button', { name: 'Delete' }).click();

		const dialog = page.getByRole('dialog', { name: 'Move to Trash?' });
		await expect(dialog).toBeVisible();
		await expect(dialog).toContainText('undo');
		await dialog.getByRole('button', { name: 'Move to Trash' }).click();
		await expect(toast(page, 'Moved to Trash')).toBeVisible();
	});
});

test('flagging needs no confirmation and shows at once', async ({ page }) => {
	await page.goto('/m/m1');
	await page.getByRole('button', { name: 'Flag' }).click();
	await expect(toast(page, 'Flagged')).toBeVisible();
	await expect(page.getByRole('button', { name: 'Unflag' })).toBeVisible();
});

test('the queue shows an action while it is waiting', async ({ page }) => {
	await page.goto('/m/m1');
	await page.getByRole('button', { name: 'Archive' }).click();
	await page.getByRole('dialog', { name: 'Archive this message?' }).getByRole('button', { name: 'Archive' }).click();
	await expect(toast(page, 'Archived')).toBeVisible();

	await page.goto('/settings/outbox');
	await expect(page.getByText('Waiting')).toBeVisible();
	await expect(page.getByText('Move to another folder')).toBeVisible();
});
