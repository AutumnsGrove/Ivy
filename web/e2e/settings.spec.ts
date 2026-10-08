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

test.describe('self-update', () => {
	test('the Update button asks the host watcher and shows progress', async ({ page }) => {
		await page.goto('/settings');
		await expect(page.getByText('Version r1.test')).toBeVisible();
		const update = page.getByRole('button', { name: 'Update' });
		await expect(update).toBeEnabled();
		await update.click();
		await expect(page.getByRole('button', { name: 'Updating…' })).toBeDisabled();
	});
});

// A switch that does not stick is worse than none: smart features turn on a
// remote service, so the choice has to be saved and shown back truthfully.
test.describe('smart features per account', () => {
	test('turning one on is saved, and still on after a reload', async ({ page }) => {
		await page.goto('/settings');
		const sw = page.getByRole('switch', { name: 'Smart features for support@example.com' });
		await expect(sw).not.toBeChecked();

		await sw.click();
		await expect(page.getByText('Smart features on for support@')).toBeVisible();

		await page.reload();
		await expect(page.getByRole('switch', { name: 'Smart features for support@example.com' })).toBeChecked();
	});

	test('turning one off is saved too', async ({ page }) => {
		await page.goto('/settings');
		await page.getByRole('switch', { name: 'Smart features for me@example.com' }).click();
		await page.reload();
		await expect(page.getByRole('switch', { name: 'Smart features for me@example.com' })).not.toBeChecked();
	});

	test('a refused change flips back and says why instead of looking saved', async ({ page }) => {
		await page.goto('/settings?scenario=smart-yaml');
		const sw = page.getByRole('switch', { name: 'Smart features for support@example.com' });
		await sw.click();
		await expect(page.getByText(/ivy\.yaml/)).toBeVisible();
		await expect(sw).not.toBeChecked();
	});
});
