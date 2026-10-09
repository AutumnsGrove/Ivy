import { expect, test } from './api';

// The reader must hold still while it is open: the hub hints on every change and
// the read mark lands 1.5s after opening, and neither may rebuild the body frame
// (a rebuilt frame repaints from nothing, which is the flicker).
test('a hub hint and the read mark leave the open body frame alone', async ({ page }) => {
	let release: (() => void) | undefined;
	const held = new Promise<void>((resolve) => {
		release = resolve;
	});
	await page.route('**/api/v1/events', async (route) => {
		await held;
		await route.fulfill({
			status: 200,
			contentType: 'text/event-stream',
			body: 'retry: 30000\nevent: outbox.state\ndata: {"type":"outbox.state","accountId":"a1"}\n\n'
		});
	});
	const seen = page.waitForRequest((r) => r.method() === 'POST' && r.url().endsWith('/api/v1/outbox'));

	await page.goto('/m/m1');
	const frame = page.frameLocator('iframe.body');
	await expect(frame.getByText('I found Grove through a friend')).toBeVisible();
	// A property on the live element survives a refetch but not a remount.
	await page.locator('iframe.body').evaluate((el) => ((el as HTMLElement & { probe?: number }).probe = 1));

	await seen;
	release?.();
	await page.waitForTimeout(1200);

	expect(await page.locator('iframe.body').evaluate((el) => (el as HTMLElement & { probe?: number }).probe)).toBe(1);
});

// An email is wider than a phone more often than not, and a frame that scrolls
// inside the page is the "tiny viewport": the frame grows to its content, and
// anything fixed-width is shrunk to fit instead of being clipped.
test('a wide fixed-width mail is shrunk to the frame and the frame grows to it', async ({ page }) => {
	// The usual newsletter: a 700px column with a block of tall content under it.
	const wide = `<!doctype html><html><body style="margin:0"><table width="700" style="width:700px"><tr><td>
		<p>Wide column</p><div style="height:4000px">tall</div></td></tr></table></body></html>`;
	await page.route('**/api/v1/messages/m1/body*', (route) =>
		route.fulfill({ status: 200, contentType: 'text/html; charset=utf-8', body: wide })
	);
	await page.goto('/m/m1');
	await expect(page.frameLocator('iframe.body').getByText('Wide column')).toBeVisible();

	const fit = () =>
		page.locator('iframe.body').evaluate((el) => {
			const f = el as HTMLIFrameElement;
			const doc = f.contentDocument!.documentElement;
			return { frameH: f.getBoundingClientRect().height, docH: doc.scrollHeight, overflowX: doc.scrollWidth - f.clientWidth };
		});
	// No inner scroll: the frame is as tall as what is in it (and taller than a
	// screenful here), and nothing sticks out sideways.
	await expect.poll(async () => (await fit()).frameH).toBeGreaterThan(1000);
	const after = await fit();
	expect(Math.abs(after.frameH - after.docH)).toBeLessThanOrEqual(2);
	expect(after.overflowX).toBeLessThanOrEqual(1);
});
