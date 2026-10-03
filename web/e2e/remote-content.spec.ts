import { expect, test } from './api';

// TESTING.md section 3: the reader must render server-sanitised mail without
// leaving the origin. The server strips remote content (proved in the Go tests,
// render/ and sync/); this checks the frame the browser builds. The body is its
// own same-origin document at /messages/{id}/body, so the policy arrives as a
// response header Chromium enforces (a <meta> CSP inside a frame is ignored by
// Chromium and srcdoc subresources are invisible to Playwright).
test('the reader renders the sanitised body without leaving the origin', async ({ page }) => {
	const external: string[] = [];
	page.on('request', (r) => {
		const url = new URL(r.url());
		if (url.hostname !== 'localhost' && url.hostname !== '127.0.0.1') external.push(r.url());
	});

	await page.goto('/m/m1');

	const frame = page.frameLocator('iframe.body');
	await expect(frame.getByText('I found Grove through a friend')).toBeVisible();
	// A loaded inline image proves the frame is live; a blocked one would have a
	// natural width of zero. Wait for the load: the body is now fetched before
	// the frame exists.
	await expect
		.poll(() => frame.locator('img').evaluate((img) => (img as HTMLImageElement).naturalWidth))
		.toBeGreaterThan(0);

	await page.waitForTimeout(300);
	expect(external).toEqual([]);
});
