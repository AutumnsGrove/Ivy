import { expect, test } from './api';
import { toast } from './helpers';

// 1x1 transparent PNG, so the upload passes the server's sniff and the badge
// has a real decodable image to show.
const PNG = Buffer.from(
	'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+M8AAAMBAQDJ/pLvAAAAAElFTkSuQmCC',
	'base64'
);

test.describe('account customization', () => {
	test('renames an account and the name survives a reload', async ({ page }) => {
		await page.goto('/settings/account/a1');
		await page.getByLabel('Name').fill('Autumn');
		await page.getByRole('button', { name: 'Save changes' }).click();
		await expect(toast(page, 'Account saved')).toBeVisible();

		await page.reload();
		await expect(page.getByLabel('Name')).toHaveValue('Autumn');
		await expect(page.getByText('Autumn').first()).toBeVisible();
	});

	test('choosing an icon shows it on the badge', async ({ page }) => {
		await page.goto('/settings/account/a1');
		await page.getByRole('button', { name: 'Icon 🌿' }).click();
		await page.getByRole('button', { name: 'Save changes' }).click();
		await expect(toast(page, 'Account saved')).toBeVisible();
		await expect(page.locator('.head .av')).toHaveText('🌿');
	});

	test('a photo uploads, previews, and can be removed', async ({ page }) => {
		await page.goto('/settings/account/a1');
		await page.getByLabel('Choose a photo').setInputFiles({ name: 'me.png', mimeType: 'image/png', buffer: PNG });
		await expect(toast(page, 'Photo updated')).toBeVisible();
		await expect(page.locator('.head img.av')).toBeVisible();

		await page.getByRole('button', { name: 'Remove' }).click();
		await expect(toast(page, 'Photo removed')).toBeVisible();
		await expect(page.getByRole('button', { name: 'Remove' })).toHaveCount(0);
	});
});
