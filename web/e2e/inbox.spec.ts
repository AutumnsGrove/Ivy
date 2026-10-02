import { expect, test } from '@playwright/test';

test.describe('inbox', () => {
	test('lists mail with the needs-you summary', async ({ page }) => {
		await page.goto('/');
		await expect(page.getByRole('heading', { name: 'Inbox' })).toBeVisible();
		await expect(page.getByText('2 need you · 2 unread')).toBeVisible();
		await expect(page.getByText('Moving my blog over to Grove?').first()).toBeVisible();
		await expect(page.getByText('Wildflower Weekly').first()).toBeVisible();
	});

	test('shows an all-caught-up screen when the inbox is empty, pointing at Reading', async ({ page }) => {
		await page.goto('/?scenario=empty');
		await expect(page.getByRole('heading', { name: 'All caught up' })).toBeVisible();
		await expect(page.getByRole('link', { name: /waiting in Reading/ })).toBeVisible();
	});

	test("says what's wrong when an account can't sign in, and what is safe", async ({ page }) => {
		await page.goto('/?scenario=sync-error');
		const banner = page.getByRole('status').filter({ hasText: "can't sign in" });
		await expect(banner).toContainText('Your mail is safe');
		await expect(banner.getByRole('button', { name: 'Fix' })).toBeVisible();
	});

	test("shows a calm 'Can't reach Ivy' screen when the server is away", async ({ page }) => {
		await page.goto('/?scenario=offline');
		await expect(page.getByRole('heading', { name: "Can't reach Ivy" })).toBeVisible();
		await expect(page.getByText('Your mail is safe')).toBeVisible();
	});
});

test.describe('reading a message', () => {
	// On desktop the list sits beside the message and repeats some words, so scope to the message region.
	const reader = (page: import('@playwright/test').Page) => page.getByRole('region', { name: 'Message', exact: true });

	test('opens the full message with its summary chip, attachments and actions', async ({ page }) => {
		await page.goto('/m/m1');
		const msg = reader(page);
		await expect(msg.getByRole('heading', { name: 'Moving my blog over to Grove?' })).toBeVisible();
		await expect(msg.getByText('Mara wonders if she can bring her old posts')).toBeVisible();
		await expect(msg.getByText('blog-export.zip')).toBeVisible();
		await expect(page.getByRole('link', { name: 'Reply' }).first()).toBeVisible();
	});

	test('archiving gives feedback with an undo', async ({ page }) => {
		await page.goto('/m/m1');
		await page.getByRole('button', { name: 'Archive' }).click();
		const toast = page.getByRole('status').filter({ hasText: 'Archived' });
		await expect(toast.getByRole('button', { name: 'Undo' })).toBeVisible();
	});

	test("keeps the header and says so when the body can't load", async ({ page }) => {
		await page.goto('/m/m1?scenario=fetch-error');
		const msg = reader(page);
		await expect(msg.getByRole('heading', { name: 'Moving my blog over to Grove?' })).toBeVisible();
		await expect(msg.getByText("This message didn't load")).toBeVisible();
		await msg.getByRole('button', { name: 'Show what we have' }).click();
		await expect(msg.getByText(/A friend pointed me to Grove/)).toBeVisible();
	});

	test('marks one attachment as failed while the rest still show', async ({ page }) => {
		await page.goto('/m/m1?scenario=attachment-error');
		const msg = reader(page);
		await expect(msg.getByText("Couldn't load")).toBeVisible();
		await expect(msg.getByText('blog-export.zip')).toBeVisible();
		await expect(msg.getByRole('button', { name: 'Retry' })).toBeVisible();
	});
});

test.describe('phone layout', () => {
	test.skip(({ viewport }) => (viewport?.width ?? 0) >= 900, 'phone only');

	test('has the five-tab bar on the inbox, with Inbox current', async ({ page }) => {
		await page.goto('/');
		const nav = page.getByRole('navigation', { name: 'Main' });
		await expect(nav.getByRole('link')).toHaveText(['Inbox', 'Reading', 'Search', 'Tags', 'Settings']);
		await expect(nav.getByRole('link', { name: 'Inbox' })).toHaveAttribute('aria-current', 'page');
	});

	test('takes the full screen for a message: no tab bar, a back button instead', async ({ page }) => {
		await page.goto('/');
		await page.getByRole('link', { name: /Moving my blog/ }).click();
		await expect(page).toHaveURL(/\/m\/m1/);
		await expect(page.getByRole('navigation', { name: 'Main' })).toHaveCount(0);
		await page.getByRole('link', { name: 'Back' }).click();
		await expect(page).toHaveURL(/\/$/);
	});

	test('opens the account side panel from the header', async ({ page }) => {
		await page.goto('/');
		await page.getByRole('button', { name: /Switch account/ }).click();
		const panel = page.getByRole('complementary', { name: 'Accounts and folders' });
		await expect(panel.getByRole('link', { name: /me@/ })).toBeVisible();
		await page.keyboard.press('Escape');
		await expect(panel).toHaveCount(0);
	});
});

test.describe('desktop layout', () => {
	test.skip(({ viewport }) => (viewport?.width ?? 0) < 900, 'desktop only');

	test('shows accounts, the list and the first message together', async ({ page }) => {
		await page.goto('/');
		await expect(page.getByRole('complementary', { name: 'Accounts and folders' })).toBeVisible();
		await expect(page.getByRole('region', { name: 'Messages' })).toBeVisible();
		const reader = page.getByRole('region', { name: 'Message', exact: true });
		await expect(reader.getByRole('heading', { name: 'Moving my blog over to Grove?' })).toBeVisible();
	});

	test('switches the reading pane when another message is chosen', async ({ page }) => {
		await page.goto('/');
		await page.getByRole('button', { name: /Notice of alleged infringement/ }).click();
		await expect(page).toHaveURL(/\/m\/m2/);
		const reader = page.getByRole('region', { name: 'Message', exact: true });
		await expect(reader.getByRole('heading', { name: 'Notice of alleged infringement' })).toBeVisible();
	});
});
