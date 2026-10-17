import { expect, test } from './api';
import { toast } from './helpers';

// 5d.4: a message in Junk that looks real says so quietly, and Not junk is the only way
// it leaves: confirmed first, then one move through the outbox. On phone and desktop.
test.describe('junk rescue', () => {
	test('a message that looks real offers Not junk, and ordinary mail does not', async ({ page }) => {
		await page.goto('/m/m2');
		await expect(page.getByText('This looks like real mail that was filed in Junk.')).toBeVisible();
		await expect(page.getByRole('button', { name: 'Not junk' })).toBeVisible();

		await page.goto('/m/m1');
		await expect(page.getByText('This looks like real mail that was filed in Junk.')).toHaveCount(0);
	});

	test('opening it moves nothing, and Not junk asks before one move to the Inbox', async ({ page }) => {
		const posts: { action?: string; messageId?: string }[] = [];
		page.on('request', (r) => {
			if (r.method() === 'POST' && r.url().endsWith('/api/v1/outbox')) posts.push(r.postDataJSON());
		});
		await page.goto('/m/m2');
		await page.waitForTimeout(500);
		expect(posts).toEqual([]);

		await page.getByRole('article').getByRole('button', { name: 'Not junk' }).click();
		const dialog = page.getByRole('dialog', { name: 'Not junk?' });
		await expect(dialog).toBeVisible();
		await dialog.getByRole('button', { name: 'Cancel' }).click();
		expect(posts).toEqual([]);

		await page.getByRole('article').getByRole('button', { name: 'Not junk' }).click();
		await page.getByRole('dialog', { name: 'Not junk?' }).getByRole('button', { name: 'Not junk' }).click();
		await expect(toast(page, 'Moved to Inbox')).toBeVisible();
		expect(posts.map((p) => [p.action, p.messageId])).toEqual([['not_junk', 'm2']]);
	});
});
