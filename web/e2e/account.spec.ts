import { expect, test } from './api';
import { toast } from './helpers';

// A valid 1x1 transparent PNG. It must really decode: the browser resizes the photo before
// uploading, and Chromium rejects an image that only has the right magic bytes.
const PNG = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAAC0lEQVR4nGNgAAIAAAUAAXpeqz8AAAAASUVORK5CYII=', 'base64');

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

	test('a big wide photo is uploaded as a small square', async ({ page }) => {
		await page.goto('/settings/account/a1');
		// Made in the page so the test needs no fixture file: 3000 x 1500, far over the edge limit.
		const wide = await page.evaluate(async () => {
			const c = document.createElement('canvas');
			c.width = 3000;
			c.height = 1500;
			c.getContext('2d')!.fillRect(0, 0, 3000, 1500);
			const blob: Blob = await new Promise((r) => c.toBlob((b) => r(b!), 'image/png')!);
			return Array.from(new Uint8Array(await blob.arrayBuffer()));
		});

		const upload = page.waitForRequest((r) => r.method() === 'PUT' && r.url().includes('/photo'));
		await page.getByLabel('Choose a photo').setInputFiles({ name: 'wide.png', mimeType: 'image/png', buffer: Buffer.from(wide) });
		const body = (await upload).postDataBuffer()!;

		// PNG IHDR: width and height are the big-endian words at bytes 16 and 20.
		expect(body.readUInt32BE(16)).toBe(512);
		expect(body.readUInt32BE(20)).toBe(512);
		await expect(toast(page, 'Photo updated')).toBeVisible();
	});

	test('a file the browser cannot read says so and uploads nothing', async ({ page }) => {
		await page.goto('/settings/account/a1');
		let puts = 0;
		page.on('request', (r) => r.method() === 'PUT' && puts++);
		await page.getByLabel('Choose a photo').setInputFiles({ name: 'broken.jpg', mimeType: 'image/jpeg', buffer: Buffer.from('not an image') });
		await expect(toast(page, "can't read that photo")).toBeVisible();
		expect(puts).toBe(0);
	});
});
