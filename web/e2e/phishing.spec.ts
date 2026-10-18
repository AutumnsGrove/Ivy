import { expect, test } from './api';

// 5d.6: a message the kept answer distrusts shows a plain warning above its subject. It only
// adds caution: the page offers nothing that moves, hides or blocks the mail. Phone and desktop.
test.describe('phishing warning', () => {
	test('a distrusted message warns, and ordinary mail does not', async ({ page }) => {
		const posts: string[] = [];
		page.on('request', (r) => {
			if (r.method() !== 'GET') posts.push(r.url());
		});
		await page.goto('/m/m4');
		const article = page.getByRole('article');
		await expect(article.getByRole('status').filter({ hasText: 'This looks like a scam' })).toBeVisible();
		await page.waitForTimeout(500);
		expect(posts.filter((u) => u.endsWith('/api/v1/outbox') && !u.includes('seen'))).toEqual([]);

		await page.goto('/m/m1');
		await expect(page.getByRole('article').getByText('This looks like a scam')).toHaveCount(0);
	});
});
