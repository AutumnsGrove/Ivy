import { expect, test } from './api';

// The hub is hints only (round 37): a `message.changed` frame must make the open
// screen refetch, with no reload. This exercises the real client code; only the
// stream source is the mock suite's.
test('a server hint refetches what is on screen', async ({ page }) => {
	let inboxRequests = 0;
	page.on('request', (request) => {
		if (request.url().includes('/api/v1/inbox')) inboxRequests++;
	});

	// Hold the hub response until the first paint, then send one hint. Otherwise
	// the hint can land before the inbox has loaded and the count proves nothing.
	let release: (() => void) | undefined;
	const held = new Promise<void>((resolve) => {
		release = resolve;
	});
	await page.route('**/api/v1/events', async (route) => {
		await held;
		await route.fulfill({
			status: 200,
			contentType: 'text/event-stream',
			body: 'retry: 30000\nevent: message.changed\ndata: {"type":"message.changed","accountId":"a1"}\n\n'
		});
	});

	await page.goto('/');
	await expect(page.getByRole('heading', { name: 'Inbox', level: 1 })).toBeVisible();
	const before = inboxRequests;
	release?.();
	await expect.poll(() => inboxRequests).toBeGreaterThan(before);
});
