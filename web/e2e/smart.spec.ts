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

	test('categories ship off and can be switched on for one account', async ({ page }) => {
		await page.goto('/settings/smart');
		const mine = page.getByRole('switch', { name: 'Categories for me@example.com' });
		await expect(mine).not.toBeChecked();
		await mine.click();
		await expect(mine).toBeChecked();
		await page.reload();
		await expect(mine).toBeChecked();
		await expect(page.getByRole('switch', { name: 'Categories for hello@example.com' })).not.toBeChecked();
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

	test.describe('when nothing can run', () => {
		test('says so, per account, with what to do about it', async ({ page }) => {
			await page.goto('/settings/smart?scenario=no-key');
			// me@ and hello@ opted in, so each is told; support@ has smart features off and is not.
			const notices = page.getByRole('status').filter({ hasText: 'OPENROUTER_API_KEY' });
			await expect(notices).toHaveCount(2);
			await expect(notices.first()).toContainText('restart Ivy');
			await expect(page.getByText("can't run").first()).toBeVisible();
		});

		test('stays quiet when nothing is in the way', async ({ page }) => {
			await page.goto('/settings/smart');
			await expect(page.getByRole('heading', { name: 'Smart features', level: 1 })).toBeVisible();
			await expect(page.getByText('OPENROUTER_API_KEY')).toHaveCount(0);
		});

		test('will not start a look over existing mail', async ({ page }) => {
			await page.goto('/settings/smart?scenario=no-key');
			await expect(page.getByRole('button', { name: 'Look over mail for me@example.com' })).toBeDisabled();
		});
	});

	test.describe('looking over existing mail', () => {
		const look = (page: import('@playwright/test').Page, who = 'me@example.com') =>
			page.getByRole('button', { name: `Look over mail for ${who}` });

		test('offers it only to accounts with smart features on', async ({ page }) => {
			await page.goto('/settings/smart');
			await expect(look(page)).toBeVisible();
			await expect(look(page, 'hello@example.com')).toBeVisible();
			await expect(look(page, 'support@example.com')).toHaveCount(0);
		});

		test('says what it will read and the most it can cost before anything starts', async ({ page }) => {
			await page.goto('/settings/smart');
			await page.getByRole('combobox', { name: 'How far back, me@example.com' }).selectOption('30d');
			await look(page).click();
			const dialog = page.getByRole('dialog');
			await expect(dialog).toContainText('12 messages');
			await expect(dialog).toContainText('$0.01');
			// Backing out starts nothing.
			await dialog.getByRole('button', { name: 'Cancel' }).click();
			await expect(toast(page, /Looking over/)).toHaveCount(0);
		});

		test('starts when confirmed, and says how many messages', async ({ page }) => {
			await page.goto('/settings/smart');
			await page.getByRole('combobox', { name: 'How far back, me@example.com' }).selectOption('all');
			await look(page).click();
			await page.getByRole('dialog').getByRole('button', { name: 'Start' }).click();
			await expect(toast(page, 'Looking over 41 messages')).toBeVisible();
		});

		test('shows how many messages are left while it works, and stops when it is done', async ({ page }) => {
			await page.goto('/settings/smart');
			await page.getByRole('combobox', { name: 'How far back, me@example.com' }).selectOption('30d');
			await look(page).click();
			await page.getByRole('dialog').getByRole('button', { name: 'Start' }).click();
			await expect(page.getByText('12 messages left to look at')).toBeVisible();
			// No second look-over can be started on top of the first.
			await expect(look(page)).toBeDisabled();
			// The screen polls while anything is waiting, and settles when the queue is empty.
			await expect(page.getByText(/left to look at/)).toHaveCount(0, { timeout: 15_000 });
			await expect(look(page)).toBeEnabled();
		});

		test('warns when it will not fit under a cap, and still lets the caps do their job', async ({ page }) => {
			await page.goto('/settings/smart?scenario=over-cap');
			await look(page).click();
			await expect(page.getByRole('dialog')).toContainText('over a monthly cap');
		});
	});
});
