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

// Issue #9: flagging is occasional, so it lives in More with the other occasional
// actions and the header stays about who the mail is from and when.
test('flagging is in More, needs no confirmation and shows at once', async ({ page }) => {
	await page.goto('/m/m1');
	await expect(page.locator('article.msg').getByRole('button', { name: /^(Flag|Unflag)$/ })).toHaveCount(0);

	await page.getByRole('button', { name: 'More' }).click();
	await page.getByRole('button', { name: 'Flag', exact: true }).click();
	await expect(toast(page, 'Flagged')).toBeVisible();

	await page.getByRole('button', { name: 'More' }).click();
	await expect(page.getByRole('button', { name: 'Remove flag' })).toBeVisible();
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

test.describe('Trash', () => {
	test('lists the trashed mail and empties it only after a confirmation', async ({ page }) => {
		await page.goto('/?folder=trash');
		await expect(page.getByRole('heading', { name: 'Trash' })).toBeVisible();

		await page.getByRole('button', { name: 'Empty Trash' }).click();
		const dialog = page.getByRole('dialog', { name: 'Permanently delete 2 messages?' });
		await expect(dialog).toBeVisible();
		await dialog.getByRole('button', { name: 'Cancel' }).click();
		await expect(dialog).toHaveCount(0);
		await expect(toast(page, 'Deleting 2 messages forever')).toHaveCount(0);

		await page.getByRole('button', { name: 'Empty Trash' }).click();
		await page.getByRole('dialog', { name: 'Permanently delete 2 messages?' }).getByRole('button', { name: 'Empty Trash' }).click();
		await expect(toast(page, 'Deleting 2 messages forever')).toBeVisible();
	});

	test('an empty Archive says so and offers no Empty Trash', async ({ page }) => {
		await page.goto('/?folder=archive');
		await expect(page.getByText('Archive is empty.')).toBeVisible();
		await expect(page.getByRole('button', { name: 'Empty Trash' })).toHaveCount(0);
	});
});

// Issue #7: reading marks the message read, through the outbox, after a dwell.
test('opening an unread message queues a seen op after a short dwell', async ({ page }) => {
	const seen = page.waitForRequest(
		(r) => r.method() === 'POST' && r.url().endsWith('/api/v1/outbox') && r.postDataJSON()?.action === 'seen'
	);
	await page.goto('/m/m1');
	const req = await seen;
	expect(req.postDataJSON()).toMatchObject({ messageId: 'm1', action: 'seen' });
});
