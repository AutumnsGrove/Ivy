import AxeBuilder from '@axe-core/playwright';
import { expect, state, test } from './api';

// 5c.0: a message the safety checks withheld says so quietly in the reader, with the
// reason, and the operator can let helpers read it and take that back. Mail the
// checks found nothing in (or have not looked at) shows nothing at all. The fixture
// withholds m3 for an instruction-like text.

test.describe('the withheld marker', () => {
	test('says why a message is kept private', async ({ page }) => {
		await page.goto('/m/m3');
		const note = page.getByRole('region', { name: 'Privacy' });
		await expect(note).toContainText('Kept private from helpers: it reads like it is giving instructions to a program.');
	});

	test('stays out of the way of mail nothing was found in', async ({ page }) => {
		// Wait for the answer itself, or the absence below would hold only because the
		// request had not come back yet.
		const answered = page.waitForResponse((r) => r.url().endsWith('/messages/m2/privacy'));
		await page.goto('/m/m2');
		await answered;
		await expect(page.getByRole('region', { name: 'Privacy' })).toHaveCount(0);
	});

	test('lets the operator allow helpers to read it, and take that back', async ({ page }) => {
		await page.goto('/m/m3');
		const note = page.getByRole('region', { name: 'Privacy' });
		await note.getByRole('button', { name: 'Let helpers read it' }).click();
		await expect(note).toContainText('Helpers can read this one because you allowed it.');
		expect(state.current.privacyAllowed.has('m3')).toBe(true);

		await note.getByRole('button', { name: 'Keep it private again' }).click();
		await expect(note).toContainText('Kept private from helpers');
		expect(state.current.privacyAllowed.has('m3')).toBe(false);
	});

	test('has no accessibility violations', async ({ page }) => {
		await page.goto('/m/m3');
		await expect(page.getByRole('region', { name: 'Privacy' })).toBeVisible();
		const results = await new AxeBuilder({ page }).exclude('iframe').analyze();
		expect(results.violations.map((v) => ({ id: v.id, targets: v.nodes.map((n) => n.target.join(' ')) }))).toEqual([]);
	});
});
