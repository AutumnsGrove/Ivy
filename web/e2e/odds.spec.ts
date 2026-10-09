import AxeBuilder from '@axe-core/playwright';
import type { Page } from '@playwright/test';
import { expect, test } from './api';

// 5b: the odds sheet is how thresholds get tuned from real mail, so it must be
// reachable on every message from the same More menu on phone and desktop.

async function openOdds(page: Page, path: string) {
	await page.goto(path);
	await page.getByRole('button', { name: 'More', exact: true }).click();
	await page.getByRole('button', { name: 'Show the odds' }).click();
	return page.getByRole('dialog', { name: 'The odds' });
}

test.describe('the odds sheet', () => {
	test('shows each question with its options, bar and standing', async ({ page }) => {
		const sheet = await openOdds(page, '/m/m1');
		const needs = sheet.getByRole('group', { name: 'Needs me' });
		await expect(needs).toContainText('Would act');
		await expect(needs).toContainText('Bar 80%');
		await expect(needs.getByRole('meter', { name: 'likely' })).toHaveAttribute('aria-valuenow', '90');
		await expect(sheet.getByRole('group', { name: 'Urgency' })).toContainText('Held back by another question');
		await expect(sheet.getByText('The answer did not fit the options, so it was thrown away')).toBeVisible();
		await expect(sheet.getByText('Nothing is moved, hidden or deleted because of them.', { exact: false })).toBeVisible();
	});

	test('explains itself on a message nothing was asked about', async ({ page }) => {
		const sheet = await openOdds(page, '/m/m2');
		await expect(sheet.getByText(/nothing has been asked about this message/i)).toBeVisible();
	});

	test('closes with Escape and leaves the message where it was', async ({ page }) => {
		const sheet = await openOdds(page, '/m/m1');
		await page.keyboard.press('Escape');
		await expect(sheet).toBeHidden();
		await expect(page).toHaveURL(/\/m\/m1$/);
	});

	test('has no accessibility violations', async ({ page }) => {
		const sheet = await openOdds(page, '/m/m1');
		await expect(sheet.getByRole('group', { name: 'Needs me' })).toBeVisible();
		const results = await new AxeBuilder({ page }).exclude('iframe').analyze();
		expect(results.violations.map((v) => ({ id: v.id, targets: v.nodes.map((n) => n.target.join(' ')) }))).toEqual([]);
	});
});
