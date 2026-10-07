import { expect, state, test } from './api';
import { toast } from './helpers';

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
	test('a refused send is calm, keeps the draft, and returns to the editor', async ({ page }) => {
		await page.goto('/compose?reply=m1&scenario=send-failed');
		await page.getByLabel('Message body').fill('Hi Mara');
		await page.getByRole('button', { name: 'Send' }).click();
		const sheet = page.getByRole('dialog', { name: 'Not sent' });
		await expect(sheet).toContainText('safe in Drafts');
		await sheet.getByRole('button', { name: 'Go back and edit' }).click();
		await expect(sheet).toBeHidden();
		await expect(page.getByLabel('Message body')).toContainText('Hi Mara');
	});

	test('sending offers undo, which hands the message back to the editor', async ({ page }) => {
		await page.goto('/compose?reply=m1');
		await page.getByLabel('Message body').fill('Undo me');
		await page.getByRole('button', { name: 'Send' }).click();
		await expect(page).toHaveURL(/\/m\/m1/);
		const undoT = toast(page, 'Sending to');
		await expect(undoT).toBeVisible();
		await undoT.getByRole('button', { name: 'Undo' }).click();
		await expect(page).toHaveURL(/\/compose\?undo=/);
		await expect(page.getByLabel('Message body')).toContainText('Undo me');
	});

	test('People autocomplete adds a recipient chip', async ({ page }) => {
		await page.goto('/compose');
		await page.getByLabel('To').fill('mara');
		await page.getByRole('option', { name: /mara@example.com/ }).click();
		await expect(page.getByRole('button', { name: 'Remove mara@example.com' })).toBeVisible();
	});

	test('the From picker offers every configured identity', async ({ page }) => {
		state.current.identities = [
			{ id: 'i2', accountId: 'a2', address: 'support@example.com', name: 'Support', signature: '', primary: false }
		];
		await page.goto('/compose?reply=m1');
		const from = page.getByLabel('From');
		await expect(from).toHaveValue('hello@example.com');
		await from.selectOption('support@example.com');
		await expect(from).toHaveValue('support@example.com');
	});

	test('rich text is the default and the mode can switch before typing', async ({ page }) => {
		await page.goto('/compose');
		const modes = page.getByRole('group', { name: 'Message format' });
		await expect(modes.getByRole('button', { name: 'Rich' })).toHaveAttribute('aria-pressed', 'true');
		await modes.getByRole('button', { name: 'Markdown' }).click();
		await expect(page.getByLabel('Message body', { exact: true })).toHaveJSProperty('tagName', 'TEXTAREA');
		await modes.getByRole('button', { name: 'Rich' }).click();
		await expect(page.getByLabel('Message body', { exact: true })).toHaveAttribute('role', 'textbox');
	});

	test('the mode is fixed once the body has content', async ({ page }) => {
		await page.goto('/compose');
		await page.getByLabel('Message body', { exact: true }).fill('Typed something');
		await expect(page.getByRole('group', { name: 'Message format' })).toHaveCount(0);
	});

	test('the format bar bolds a selection and starts a list', async ({ page }) => {
		await page.goto('/compose');
		const body = page.getByLabel('Message body', { exact: true });
		await body.fill('hello world');
		await body.selectText();
		await page.getByRole('button', { name: 'Bold' }).click();
		await expect(body.locator('b, strong')).toHaveCount(1);
		await page.getByRole('button', { name: 'List' }).click();
		await expect(body.locator('ul')).toHaveCount(1);
	});
});

test.describe('tags', () => {
	test('creating a tag needs a name, then confirms', async ({ page }) => {
		await page.goto('/tags?new');
		const sheet = page.getByRole('dialog', { name: 'New tag' });
		await expect(sheet.getByRole('button', { name: 'Create tag' })).toBeDisabled();
		await sheet.getByLabel('Name').fill('ideas');
		await sheet.getByRole('button', { name: 'Create tag' }).click();
		await expect(toast(page, 'created')).toBeVisible();
	});
});

test.describe('resizable panes (desktop)', () => {
	test.skip(({ viewport }) => (viewport?.width ?? 0) < 900, 'desktop only');

	const width = async (page: import('@playwright/test').Page, name: string) =>
		(await page.getByRole('region', { name, exact: true }).boundingBox())!.width;

	test('dragging the handle resizes the message list and the choice survives a reload', async ({ page }) => {
		await page.goto('/');
		const before = await width(page, 'Messages');
		const handle = page.getByRole('separator', { name: 'Resize message list' });
		const box = (await handle.boundingBox())!;
		await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
		await page.mouse.down();
		await page.mouse.move(box.x + box.width / 2 + 80, box.y + box.height / 2, { steps: 5 });
		await page.mouse.up();
		expect(await width(page, 'Messages')).toBeGreaterThan(before + 60);

		await page.reload();
		expect(await width(page, 'Messages')).toBeGreaterThan(before + 60);
	});

	test('keyboard resizes and double-click resets', async ({ page }) => {
		await page.goto('/');
		const handle = page.getByRole('separator', { name: 'Resize accounts pane' });
		await handle.focus();
		await page.keyboard.press('ArrowRight');
		await expect(handle).toHaveAttribute('aria-valuenow', '252');
		await handle.dblclick();
		await expect(handle).toHaveAttribute('aria-valuenow', '236');
	});

	test('the message pane keeps a readable width however far the list is dragged', async ({ page }) => {
		await page.goto('/');
		const handle = page.getByRole('separator', { name: 'Resize message list' });
		await handle.focus();
		await page.keyboard.press('End');
		expect(await width(page, 'Message')).toBeGreaterThanOrEqual(360);
	});
});
