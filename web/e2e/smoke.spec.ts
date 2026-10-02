import { expect, test } from '@playwright/test';

// The thin end-to-end slice that exists from day one (TESTING.md 0): the real
// Go binary boots, answers the contract, and serves the embedded frontend on
// both viewports. It grows with each milestone (deliver, read, flag, restart)
// as those layers land; today the backend only owns version, health and static
// assets, and the screens still read from the mock client.

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

test('renders the inbox on load', async ({ page }) => {
	const problems: string[] = [];
	page.on('pageerror', (e) => problems.push(`pageerror: ${e.message}`));
	page.on('console', (m) => {
		if (m.type() === 'error') problems.push(`console: ${m.text()}`);
	});

	await page.goto('/');
	await page.waitForLoadState('networkidle');
	await expect(page.getByRole('heading', { name: 'Inbox' })).toBeVisible();
	await expect(page.getByText('2 need you · 2 unread')).toBeVisible();
	expect(problems).toEqual([]);
});

test('client-side routes survive a cold load (SPA fallback)', async ({ page }) => {
	await page.goto('/m/m1');
	await expect(
		page.getByRole('region', { name: 'Message', exact: true }).getByRole('heading', {
			name: 'Moving my blog over to Grove?'
		})
	).toBeVisible();
});
