import { expect, test } from './api';
import { toast } from './helpers';

// 5e.2: the Waiting on list, on phone and desktop. Nothing is sent or changed by it; the
// only button is the operator's own "no answer needed".

test.describe('waiting on', () => {
	test('lists the threads you wrote last on, longest wait first', async ({ page }) => {
		await page.goto('/waiting');
		await expect(page.getByRole('heading', { name: 'Waiting on' })).toBeVisible();
		const rows = page.getByRole('link', { name: /Waiting on/ });
		await expect(rows).toHaveCount(2);
		await expect(rows.first()).toContainText('Quote for the hosting move');
		await expect(rows.first()).toContainText('Waiting on Hana Okafor, 6 days');
		await expect(rows.last()).toContainText('Waiting on Sam, kim@example.com +1 more, 3 days');
	});

	test('a row opens the message you sent', async ({ page }) => {
		await page.goto('/waiting');
		await page.getByRole('link', { name: /Quote for the hosting move/ }).click();
		await expect(page).toHaveURL(/\/m\/w1$/);
	});

	test('stop waiting removes the row and says so', async ({ page }) => {
		await page.goto('/waiting');
		await page.getByRole('button', { name: 'Stop waiting on Quote for the hosting move' }).click();
		await expect(toast(page, 'Stopped waiting')).toBeVisible();
		await expect(page.getByRole('link', { name: /Quote for the hosting move/ })).toHaveCount(0);
		await expect(page.getByRole('link', { name: /Figures for Friday/ })).toBeVisible();
	});

	test('says so when nothing is waiting, and where the switch is', async ({ page }) => {
		await page.goto('/waiting?scenario=empty');
		await expect(page.getByText('Nothing you sent is waiting on an answer.')).toBeVisible();
		await expect(page.getByRole('link', { name: 'Smart features' })).toHaveAttribute('href', '/settings/smart');
	});

	test('the nav links to it', async ({ page }) => {
		await page.goto('/');
		const link = page.getByRole('link', { name: /Waiting on/ });
		const menu = page.getByRole('button', { name: 'All inboxes' });
		// The desktop pane shows the link at once; the phone opens it from the menu button.
		await expect(link.or(menu)).toBeVisible();
		if (!(await link.isVisible())) await menu.click();
		await link.click();
		await expect(page).toHaveURL(/\/waiting$/);
	});
});
