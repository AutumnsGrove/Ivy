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

// A tag is a keyword written to the real (fake) IMAP server through the outbox,
// and the membership follows the acknowledged write (chunk 3e): this runs the
// whole path in the production binary, not just the pieces.
test('tags a message through the real outbox and clears the tag again', async ({ request }, testInfo) => {
	const inbox = await (await request.get('/api/v1/inbox')).json();
	const id: string = inbox.items[0].id;

	const created = await request.post('/api/v1/tags', { data: { name: `smoke ${testInfo.project.name}`, color: 'mint' } });
	expect(created.status()).toBe(201);
	const tag = await created.json();

	const queued = await request.post('/api/v1/outbox', { data: { messageId: id, action: 'tag', tagId: tag.id } });
	expect(queued.status()).toBe(202);
	const tagIds = async (): Promise<string[]> => ((await (await request.get(`/api/v1/messages/${id}`)).json()).tagIds ?? []) as string[];
	await expect.poll(tagIds, { timeout: 20_000 }).toContain(tag.id);

	// Deleting the tag clears its keyword from the server first, then forgets it.
	const removed = await request.delete(`/api/v1/tags/${tag.id}`);
	expect(removed.status()).toBe(204);
	expect(await tagIds()).not.toContain(tag.id);
});

// The stats panel reads the real ledger (chunk 5, 5a.2): the roll-up and the log come from the
// gateway, the totals add up, and the screens render them with nothing in the console.
test('serves the spend roll-up and call log from the real ledger', async ({ request, page }) => {
	const summary = await (await request.get('/api/v1/spend')).json();
	expect(summary).toMatchObject({ totalUsd: expect.any(Number), capUsd: expect.any(Number), byFeature: expect.any(Array), blocked: expect.any(Array) });
	const byFeature = (summary.byFeature as { usd: number }[]).reduce((n, f) => n + f.usd, 0);
	expect(byFeature).toBeCloseTo(summary.totalUsd, 9);

	const log = await request.get('/api/v1/spend/calls?limit=5');
	expect(log.status()).toBe(200);
	expect((await log.json()).items.length).toBeLessThanOrEqual(5);
	expect((await request.get('/api/v1/spend/calls?outcome=acted')).status()).toBe(400);

	const errors: string[] = [];
	page.on('console', (m) => m.type() === 'error' && errors.push(m.text()));
	await page.goto('/settings/spend');
	await expect(page.getByText(/^Spent in the last 7 days$/)).toBeVisible();
	await page.goto('/settings/spend/calls');
	await expect(page.getByRole('link', { name: 'Save as CSV' })).toBeVisible();
	expect(errors).toEqual([]);
});
