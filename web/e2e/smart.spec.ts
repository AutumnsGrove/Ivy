import { expect, test } from './api';
import { toast } from './helpers';

// The accounts in the fixture: me@ and hello@ have smart features on, support@ does not.
test.describe('smart features settings', () => {
	test('the settings home opens the screen', async ({ page }) => {
		await page.goto('/settings');
		await page.getByRole('link', { name: /Caps, features and models/ }).click();
		await expect(page).toHaveURL(/\/settings\/smart$/);
		await expect(page.getByRole('heading', { name: 'Smart features', level: 1 })).toBeVisible();
	});

	test('states every cap and what has been spent against it', async ({ page }) => {
		await page.goto('/settings/smart');
		await expect(page.getByRole('textbox', { name: 'Monthly cap, all accounts' })).toHaveValue('$10.00');
		await expect(page.getByRole('textbox', { name: 'Monthly cap, me@example.com' })).toHaveValue('$5.00');
		await expect(page.getByText(/spent this month/).first()).toBeVisible();
	});

	test('a typed cap is saved and survives a reload', async ({ page }) => {
		await page.goto('/settings/smart');
		const global = page.getByRole('textbox', { name: 'Monthly cap, all accounts' });
		await global.fill('$20');
		await global.press('Enter');
		await expect(toast(page, 'Saved')).toBeVisible();
		await page.reload();
		await expect(global).toHaveValue('$20.00');
	});

	test('zero is a real cap, and it says nothing will be spent', async ({ page }) => {
		await page.goto('/settings/smart');
		const mine = page.getByRole('textbox', { name: 'Monthly cap, me@example.com' });
		await mine.fill('0');
		await mine.press('Enter');
		await page.reload();
		await expect(mine).toHaveValue('$0.00');
		await expect(page.getByText('No spending is allowed for this account')).toBeVisible();
	});

	test('an amount Ivy will not store puts the old cap back and says why', async ({ page }) => {
		await page.goto('/settings/smart');
		const global = page.getByRole('textbox', { name: 'Monthly cap, all accounts' });
		for (const bad of ['ten', '-4', '5000']) {
			await global.fill(bad);
			await global.press('Enter');
			// Each refusal raises the same toast, so more than one may be on screen by now.
			await expect(toast(page, /between \$0 and \$1,?000/).first()).toBeVisible();
			await expect(global).toHaveValue('$10.00');
		}
		await page.reload();
		await expect(global).toHaveValue('$10.00');
	});

	test('a feature can be switched off for one account only', async ({ page }) => {
		await page.goto('/settings/smart');
		const mine = page.getByRole('switch', { name: 'Meaning search for me@example.com' });
		const other = page.getByRole('switch', { name: 'Meaning search for hello@example.com' });
		await expect(mine).toBeChecked();
		await mine.click();
		await expect(mine).not.toBeChecked();
		await page.reload();
		await expect(mine).not.toBeChecked();
		await expect(other).toBeChecked();
	});

	test('an account with smart features off cannot have a feature switched on', async ({ page }) => {
		await page.goto('/settings/smart');
		const off = page.getByRole('switch', { name: 'Meaning search for support@example.com' });
		await expect(off).toBeDisabled();
		await expect(page.getByText('Smart features are off for this account').first()).toBeVisible();
	});

	test('the default chat model can be chosen, with its price shown', async ({ page }) => {
		await page.goto('/settings/smart');
		const picker = page.getByRole('combobox', { name: 'Default chat model' });
		await expect(picker).toHaveValue('deepseek');
		await picker.selectOption('mercury');
		await page.reload();
		await expect(picker).toHaveValue('mercury');
		await expect(page.getByText(/\$0\.04 in, \$0\.15 out per million tokens/)).toBeVisible();
	});

	test('the cap on the spend screen follows the one saved here', async ({ page }) => {
		await page.goto('/settings/smart');
		const global = page.getByRole('textbox', { name: 'Monthly cap, all accounts' });
		await global.fill('7');
		await global.press('Enter');
		await expect(toast(page, 'Saved')).toBeVisible();
		await page.goto('/settings/spend');
		await expect(page.getByText(/of \$7\.00/)).toBeVisible();
	});
});
