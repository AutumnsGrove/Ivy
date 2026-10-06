import { expect, test } from './api';
import { toast } from './helpers';

// Setting up Ivy from the app: the operator types the mailbox password here and
// nowhere else. The fake gateway refuses the password `wrong`, cannot reach the
// provider for `down`, and connects anything else (see connectReply in api.ts).

test.describe('connecting an account', () => {
	test('a working address and password connect and land on the inbox', async ({ page }) => {
		await page.goto('/welcome/account');
		await page.getByLabel('Email address').fill('new@grove.test');
		await page.getByLabel('App password').fill('correct horse');
		const request = page.waitForRequest((r) => r.url().endsWith('/api/v1/accounts') && r.method() === 'POST');
		await page.getByRole('button', { name: 'Test and connect' }).click();

		expect((await request).postDataJSON()).toEqual({ address: 'new@grove.test', password: 'correct horse', smart: false });
		await expect(toast(page, 'Connected')).toBeVisible();
		await expect(page).toHaveURL('/');
	});

	test('the connect button waits for an address and a password', async ({ page }) => {
		await page.goto('/welcome/account');
		const connect = page.getByRole('button', { name: 'Test and connect' });
		await expect(connect).toBeDisabled();
		await page.getByLabel('Email address').fill('new@grove.test');
		await expect(connect).toBeDisabled();
		await page.getByLabel('App password').fill('x');
		await expect(connect).toBeEnabled();
	});

	test('a refused password says so and keeps what was typed', async ({ page }) => {
		await page.goto('/welcome/account');
		await page.getByLabel('Email address').fill('new@grove.test');
		await page.getByLabel('App password').fill('wrong');
		await page.getByRole('button', { name: 'Test and connect' }).click();

		await expect(page.getByRole('alert')).toContainText("didn't accept");
		await expect(page).toHaveURL(/\/welcome\/account/);
		await expect(page.getByLabel('Email address')).toHaveValue('new@grove.test');
		await expect(page.getByRole('button', { name: 'Test and connect' })).toBeEnabled();
	});

	test('an unreachable provider is not reported as a wrong password', async ({ page }) => {
		await page.goto('/welcome/account');
		await page.getByLabel('Email address').fill('new@grove.test');
		await page.getByLabel('App password').fill('down');
		await page.getByRole('button', { name: 'Test and connect' }).click();

		await expect(page.getByRole('alert')).toContainText("Couldn't reach");
		await expect(page.getByRole('alert')).not.toContainText("didn't accept");
	});

	test('an address that is already connected is refused', async ({ page }) => {
		await page.goto('/welcome/account');
		await page.getByLabel('Email address').fill('me@example.com');
		await page.getByLabel('App password').fill('whatever');
		await page.getByRole('button', { name: 'Test and connect' }).click();

		await expect(page.getByRole('alert')).toContainText('already connected');
	});

	test('smart features are sent as asked, and the screen says when they start', async ({ page }) => {
		await page.goto('/welcome/account');
		await page.getByLabel('Email address').fill('new@grove.test');
		await page.getByLabel('App password').fill('correct horse');
		await page.getByRole('switch', { name: 'Smart features' }).click();
		await expect(page.getByText('next time Ivy starts')).toBeVisible();
		const request = page.waitForRequest((r) => r.url().endsWith('/api/v1/accounts') && r.method() === 'POST');
		await page.getByRole('button', { name: 'Test and connect' }).click();
		expect((await request).postDataJSON()).toMatchObject({ smart: true });
	});

	test('the screen is honest that Purelymail is the one provider for now', async ({ page }) => {
		await page.goto('/welcome/account');
		await expect(page.getByText('Purelymail').first()).toBeVisible();
		await expect(page.getByText('Found your mail server')).toHaveCount(0);
	});

	test('while the login is tested the button says so and cannot be pressed twice', async ({ page }) => {
		let posts = 0;
		await page.route('**/api/v1/accounts', async (route) => {
			if (route.request().method() !== 'POST') return route.fallback();
			posts++;
			await new Promise((r) => setTimeout(r, 400));
			return route.fulfill({ status: 201, contentType: 'application/json', body: JSON.stringify({ id: 'purelymail' }) });
		});
		await page.goto('/welcome/account');
		await page.getByLabel('Email address').fill('new@grove.test');
		await page.getByLabel('App password').fill('correct horse');
		await page.getByRole('button', { name: 'Test and connect' }).click();

		const busy = page.getByRole('button', { name: /Testing/ });
		await expect(busy).toBeDisabled();
		await expect(page).toHaveURL('/');
		expect(posts).toBe(1);
	});
});

test.describe('updating a password', () => {
	test('asks only for the password and returns to Mirror health', async ({ page }) => {
		await page.goto('/welcome/account?update=a1');
		await expect(page.getByRole('heading', { name: 'Update password' })).toBeVisible();
		await expect(page.getByLabel('Email address')).toHaveCount(0);
		await expect(page.getByRole('switch', { name: 'Smart features' })).toHaveCount(0);

		const request = page.waitForRequest((r) => r.url().endsWith('/api/v1/accounts/a1/password') && r.method() === 'PUT');
		await page.getByLabel('App password').fill('new password');
		await page.getByRole('button', { name: 'Test and save' }).click();
		expect((await request).postDataJSON()).toEqual({ password: 'new password' });
		await expect(toast(page, 'Password updated')).toBeVisible();
		await expect(page).toHaveURL(/\/settings\/health/);
	});

	test('a refused new password says so and the old one stays', async ({ page }) => {
		await page.goto('/welcome/account?update=a1');
		await page.getByLabel('App password').fill('wrong');
		await page.getByRole('button', { name: 'Test and save' }).click();
		await expect(page.getByRole('alert')).toContainText("didn't accept");
		await expect(page).toHaveURL(/update=a1/);
	});

	test('Mirror health sends a failed account to its own update screen', async ({ page }) => {
		await page.goto('/settings/health');
		const link = page.getByRole('link', { name: 'Update password' }).first();
		await expect(link).toHaveAttribute('href', /\/welcome\/account\?update=a\d/);
	});
});

test.describe('first run', () => {
	test('with no accounts the app opens on the welcome screen', async ({ page }) => {
		await page.route('**/api/v1/accounts', (route) => {
			if (route.request().method() !== 'GET') return route.fallback();
			return route.fulfill({ status: 200, contentType: 'application/json', body: '[]' });
		});
		await page.goto('/');
		await expect(page).toHaveURL(/\/welcome$/);
		await expect(page.getByRole('link', { name: 'Connect your first account' })).toBeVisible();
	});
});
