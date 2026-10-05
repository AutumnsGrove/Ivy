import { expect, test } from './api';
import { toast } from './helpers';

// Tags are kept locally and as IMAP keywords (chunk 3e). Tagging a message goes
// through the outbox like every other write; the tag screens call the real API.
test.describe('tagging a message', () => {
	test('the picker lists your tags and tags the message, with undo', async ({ page }) => {
		await page.goto('/m/m1');
		await page.getByRole('button', { name: 'Tag', exact: true }).click();

		const dialog = page.getByRole('dialog', { name: 'Tag this message' });
		await expect(dialog).toBeVisible();
		const receipts = dialog.getByRole('switch', { name: 'receipts' });
		await expect(receipts).toHaveAttribute('aria-checked', 'false');

		await receipts.click();
		await expect(receipts).toHaveAttribute('aria-checked', 'true');
		const done = toast(page, 'Tagged “receipts”');
		await expect(done).toBeVisible();

		await done.getByRole('button', { name: 'Undo' }).click();
		await expect(toast(page, 'Undone')).toBeVisible();
		await expect(receipts).toHaveAttribute('aria-checked', 'false');
	});

	test('a tag the message is already in shows as on, and can be taken off', async ({ page }) => {
		await page.goto('/m/m1');
		await page.getByRole('button', { name: 'Tag', exact: true }).click();
		await page.getByRole('dialog', { name: 'Tag this message' }).getByRole('switch', { name: 'legal' }).click();
		await expect(toast(page, 'Tagged “legal”')).toBeVisible();
		await page.keyboard.press('Escape');

		await page.getByRole('button', { name: 'Tag', exact: true }).click();
		const legal = page.getByRole('dialog', { name: 'Tag this message' }).getByRole('switch', { name: 'legal' });
		await expect(legal).toHaveAttribute('aria-checked', 'true');
		await legal.click();
		await expect(toast(page, 'Removed “legal”')).toBeVisible();
		await expect(legal).toHaveAttribute('aria-checked', 'false');
	});
});

test.describe('managing tags', () => {
	test('a new tag is created through the API and listed', async ({ page }) => {
		await page.goto('/tags?new');
		const sheet = page.getByRole('dialog', { name: 'New tag' });
		await sheet.getByLabel('Name').fill('Café notes');
		await sheet.getByRole('button', { name: 'Create tag' }).click();

		await expect(toast(page, 'Tag “Café notes” created')).toBeVisible();
		await expect(page.getByRole('link', { name: /Café notes/ })).toBeVisible();
	});

	test('renaming a tag saves it and the list shows the new name', async ({ page }) => {
		await page.goto('/tags/t5');
		await page.getByLabel('Name').fill('big ideas');
		await page.getByRole('button', { name: 'Save' }).click();

		await expect(toast(page, 'Tag saved')).toBeVisible();
		await expect(page).toHaveURL(/\/tags$/);
		await expect(page.getByRole('link', { name: /big ideas/ })).toBeVisible();
	});

	test('deleting a tag asks first, says what it touches, and then removes it', async ({ page }) => {
		await page.goto('/tags/t5');
		await page.getByRole('button', { name: 'Delete tag' }).click();

		const dialog = page.getByRole('dialog', { name: 'Delete “ideas”?' });
		await expect(dialog).toContainText('2 messages');
		await dialog.getByRole('button', { name: 'Cancel' }).click();
		await expect(dialog).toHaveCount(0);

		await page.getByRole('button', { name: 'Delete tag' }).click();
		await page.getByRole('dialog', { name: 'Delete “ideas”?' }).getByRole('button', { name: 'Delete tag' }).click();
		await expect(toast(page, 'Deleted “ideas”')).toBeVisible();
		await expect(page).toHaveURL(/\/tags$/);
		await expect(page.getByRole('link', { name: /ideas/ })).toHaveCount(0);
	});
});
