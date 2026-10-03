import { expect, test } from './api';

test.describe('app icon', () => {
	test('declares a favicon, an iOS home-screen icon and a manifest', async ({ page }) => {
		await page.goto('/');
		await expect(page.locator('link[rel="icon"][href="/favicon.ico"]')).toHaveCount(1);
		await expect(page.locator('link[rel="icon"][type="image/png"][sizes="32x32"]')).toHaveCount(1);
		await expect(page.locator('link[rel="apple-touch-icon"][href="/apple-touch-icon.png"]')).toHaveCount(1);
		await expect(page.locator('link[rel="manifest"]')).toHaveCount(1);
	});

	test('serves every icon the head and manifest point at', async ({ page, request }) => {
		await page.goto('/');
		const manifest = await (await request.get('/manifest.webmanifest')).json();
		expect(manifest.name).toBe('Ivy');
		const urls = ['/favicon.ico', '/favicon-32.png', '/apple-touch-icon.png', ...manifest.icons.map((i: { src: string }) => i.src)];
		for (const url of urls) {
			const res = await request.get(url);
			expect(res.status(), url).toBe(200);
			expect(res.headers()['content-type'], url).toMatch(/image\//);
		}
	});

	test('the desktop rail and the welcome screen show the leaf', async ({ page, viewport }) => {
		if ((viewport?.width ?? 0) >= 900) {
			await page.goto('/');
			await expect(page.getByRole('navigation', { name: 'Main' }).getByRole('img', { name: 'Ivy' })).toBeVisible();
		}
		await page.goto('/welcome');
		// On desktop the rail shows its own small logo too; the welcome one is the large leaf in the page body.
		const hero = page.getByRole('img', { name: 'Ivy' }).last();
		await expect(hero).toBeVisible();
		expect((await hero.boundingBox())!.width).toBeGreaterThan(100);
	});
});
