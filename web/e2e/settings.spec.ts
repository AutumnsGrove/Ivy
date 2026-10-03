import { expect, test } from './api';

test.describe('behaviour settings', () => {
	test('a changed picker and a flipped switch survive a reload', async ({ page }) => {
		await page.goto('/settings');
		await expect(page.getByRole('combobox', { name: 'Undo send' })).toHaveValue('10');

		await page.getByRole('combobox', { name: 'Undo send' }).selectOption('30');
		await page.getByRole('combobox', { name: 'Remote images' }).selectOption('never');
		await page.getByRole('switch', { name: 'Show the spam score' }).click();

		await page.reload();
		await expect(page.getByRole('combobox', { name: 'Undo send' })).toHaveValue('30');
		await expect(page.getByRole('combobox', { name: 'Remote images' })).toHaveValue('never');
		await expect(page.getByRole('switch', { name: 'Show the spam score' })).toBeChecked();
	});

	test('the digest can be turned off and back on', async ({ page }) => {
		await page.goto('/settings');
		const digest = page.getByRole('combobox', { name: 'Daily digest time' });
		await digest.selectOption('off');
		await page.reload();
		await expect(digest).toHaveValue('off');
		await digest.selectOption('18:00');
		await page.reload();
		await expect(digest).toHaveValue('18:00');
	});
});
