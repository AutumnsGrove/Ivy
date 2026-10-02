import { expect, test } from '@playwright/test';

// TESTING.md section 3: the reader must render server-sanitised mail without
// leaving the origin. The server strips remote content (proved in the Go tests,
// render/ and sync/); this checks the frame the browser builds: the message
// text shows, its same-origin inline image really loads, and no request leaves
// the origin.
//
// Note: Chromium ignores a <meta> Content-Security-Policy inside a srcdoc frame,
// and WebKit does not report srcdoc subresource requests to `page.on('request')`.
// So the cross-browser remote-content assertion waits for the real body
// document endpoint (2f), which carries the policy as a response header.
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
	// natural width of zero.
	const width = await frame.locator('img').evaluate((img) => img.naturalWidth);
	expect(width).toBeGreaterThan(0);

	await page.waitForTimeout(300);
	expect(external).toEqual([]);
});
