import { expect, test } from '@playwright/test';

test.describe('search', () => {
	test('finds mail, highlights the words, and offers Ask Ivy for the same query', async ({ page }) => {
		await page.goto('/search');
		await page.getByRole('searchbox', { name: 'Search your mail' }).fill('domain renewal');
		await page.keyboard.press('Enter');
		await expect(page).toHaveURL(/q=domain\+renewal|q=domain%20renewal/);
		await expect(page.getByText('Best matches · 3')).toBeVisible();
		await expect(page.locator('mark').first()).toBeVisible();
		await expect(page.getByRole('link', { name: /Ask Ivy about/ })).toBeVisible();
	});

	test('says so plainly when nothing matches', async ({ page }) => {
		await page.goto('/search?q=xylophone+invoice');
		await expect(page.getByRole('heading', { name: 'Nothing found' })).toBeVisible();
	});
});

test.describe('ask ivy', () => {
	test('answers with numbered citations that match the listed sources', async ({ page }) => {
		await page.goto('/ask?q=When+does+my+domain+renew%3F');
		await expect(page.getByText('Your domain renews on the 14th')).toBeVisible();
		await expect(page.getByText('Your domain renews soon').first()).toBeVisible();
		await expect(page.locator('.cite').first()).toHaveText('1');
	});

	test('locks accounts that have smart features off, so they cannot be searched by Ask', async ({ page }) => {
		await page.goto('/ask');
		await expect(page.getByRole('button', { name: 'support' })).toBeDisabled();
		await expect(page.getByRole('button', { name: 'me' })).toBeEnabled();
	});

	test('rests gracefully at the monthly limit and offers search instead', async ({ page }) => {
		await page.goto('/ask?q=hello&scenario=limit');
		await expect(page.getByRole('heading', { name: 'Ivy is resting' })).toBeVisible();
		await expect(page.getByRole('link', { name: 'Search instead' })).toBeVisible();
	});
});

test.describe('settings', () => {
	test('switching to the day theme changes the page and is remembered after reload', async ({ page }) => {
		await page.goto('/settings');
		await expect(page.locator('html')).toHaveAttribute('data-theme', 'night');
		await page.getByRole('radio', { name: 'Day' }).click();
		await expect(page.locator('html')).toHaveAttribute('data-theme', 'day');
		await page.reload();
		await expect(page.locator('html')).toHaveAttribute('data-theme', 'day');
	});

	test('smart features are off by default for accounts that never opted in', async ({ page }) => {
		await page.goto('/settings');
		await expect(page.getByRole('switch', { name: 'Smart features for support@example.com' })).toHaveAttribute('aria-checked', 'false');
		await expect(page.getByRole('switch', { name: 'Smart features for me@example.com' })).toHaveAttribute('aria-checked', 'true');
	});
});

test.describe('compose', () => {
	test('a too-large send is refused calmly, keeps the draft, and can be fixed in one tap', async ({ page }) => {
		await page.goto('/compose?reply=m1&scenario=send-failed');
		await page.getByRole('button', { name: 'Send' }).click();
		const sheet = page.getByRole('dialog', { name: 'Not sent' });
		await expect(sheet).toContainText('safe in Drafts');
		await sheet.getByRole('button', { name: /Remove blog-export.zip and send/ }).click();
		await expect(page).toHaveURL(/\/$/);
		await expect(page.getByRole('status').filter({ hasText: 'Sending to' })).toBeVisible();
	});

	test('sending offers undo', async ({ page }) => {
		await page.goto('/compose?reply=m1');
		await page.getByRole('button', { name: 'Send' }).click();
		await expect(page.getByRole('button', { name: 'Undo' })).toBeVisible();
	});
});

test.describe('tags', () => {
	test('creating a tag needs a name, then confirms', async ({ page }) => {
		await page.goto('/tags?new');
		const sheet = page.getByRole('dialog', { name: 'New tag' });
		await expect(sheet.getByRole('button', { name: 'Create tag' })).toBeDisabled();
		await sheet.getByLabel('Name').fill('ideas');
		await sheet.getByRole('button', { name: 'Create tag' }).click();
		await expect(page.getByRole('status').filter({ hasText: 'created' })).toBeVisible();
	});
});
