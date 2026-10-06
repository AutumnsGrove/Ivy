import { expect, state, test } from './api';

// /welcome is where a bookmark taken on the wrong screen lands. With an account
// connected it must offer a way straight to the inbox; on a fresh install it must
// stay the plain first-run screen.

test.describe('the welcome screen', () => {
	test('offers the inbox first when an account is already connected', async ({ page }) => {
		await page.goto('/welcome');
		const inbox = page.getByRole('link', { name: 'Take me to my inbox' });
		await expect(inbox).toBeVisible();
		await expect(page.getByRole('link', { name: 'Connect another account' })).toBeVisible();
		await expect(page.getByRole('link', { name: 'Connect your first account' })).toHaveCount(0);

		await inbox.click();
		await expect(page).toHaveURL('/');
	});

	test('the inbox button sits above the logo', async ({ page }) => {
		await page.goto('/welcome');
		const inbox = await page.getByRole('link', { name: 'Take me to my inbox' }).boundingBox();
		const logo = await page.getByRole('main').getByRole('img', { name: 'Ivy' }).boundingBox();
		expect(inbox && logo && inbox.y < logo.y).toBe(true);
	});

	test('a fresh install only offers to connect the first account', async ({ page }) => {
		state.current.accounts = [];
		await page.goto('/welcome');
		await expect(page.getByRole('link', { name: 'Connect your first account' })).toBeVisible();
		await expect(page.getByRole('link', { name: 'Take me to my inbox' })).toHaveCount(0);
	});
});
