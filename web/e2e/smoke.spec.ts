import { expect, test } from '@playwright/test';

// The thin end-to-end slice that exists from day one (TESTING.md 0): the real
// Go binary boots, answers the contract, and serves the embedded frontend on
// both viewports. It grows with each milestone (deliver, read, flag, restart)
// as those layers land; the reader now calls the real read API, so the screens
// render the gateway's answer (the empty mirror until sync is wired).

test('boots and answers the API contract', async ({ request }) => {
	const version = await request.get('/api/v1/version');
	expect(version.status()).toBe(200);
	expect(await version.json()).toMatchObject({ version: expect.any(String) });

	const health = await request.get('/api/v1/health');
	expect(health.status()).toBe(200);
	expect(await health.json()).toMatchObject({ status: 'ok' });
});

test('serves the precompressed frontend with the right caching headers', async ({ request }) => {
	const index = await request.get('/');
	expect(index.status()).toBe(200);
	expect(index.headers()['content-type']).toContain('text/html');
	expect(index.headers()['vary']).toContain('Accept-Encoding');

	// Pull one hashed bundle out of the shell and prove the server negotiates a
	// precompressed sibling for it, and caches it immutably.
	const html = await index.text();
	const asset = html.match(/(\/_app\/immutable\/[^"']+\.js)/)?.[1];
	expect(asset, 'index.html references a hashed JS bundle').toBeTruthy();
	const bundle = await request.get(asset!, { headers: { 'Accept-Encoding': 'zstd' } });
	expect(bundle.status()).toBe(200);
	expect(bundle.headers()['content-encoding']).toBe('zstd');
	expect(bundle.headers()['cache-control']).toContain('immutable');
});

test('renders the seeded mailbox from the real read API', async ({ page, request }) => {
	const problems: string[] = [];
	page.on('pageerror', (e) => problems.push(`pageerror: ${e.message}`));
	page.on('console', (m) => {
		if (m.type() === 'error') problems.push(`console: ${m.text()}`);
	});

	await page.goto('/');
	await page.waitForLoadState('networkidle');
	// ivy-dev syncs the minimal profile before it serves, so the inbox has mail;
	// the screen must show what the API returns, not the caught-up state.
	const inbox = await (await request.get('/api/v1/inbox')).json();
	expect(inbox.items.length).toBeGreaterThan(0);
	await expect(page.getByRole('heading', { name: 'Inbox', level: 1 })).toBeVisible();
	await expect(page.getByRole('heading', { name: 'All caught up' })).toHaveCount(0);
	await expect(page.getByRole('list').first().getByRole('listitem').first()).toBeVisible();
	expect(problems).toEqual([]);
});

test('client-side routes survive a cold load (SPA fallback)', async ({ page }) => {
	await page.goto('/settings');
	await expect(page.getByRole('heading', { name: 'Settings' })).toBeVisible();
});
