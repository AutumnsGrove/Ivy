import { expect, test } from './api';

// Every list is served a page at a time; the screens must reach the rest. `?scenario=paged` makes
// the fake gateway return the first rows with a `nextCursor`, as the real one does.
test.describe('paged lists', () => {
	test('the inbox shows the newest page and "Show older" appends the rest', async ({ page }) => {
		await page.goto('/?scenario=paged');
		await expect(page.getByText('[Lattice] Pull request merged into main').first()).toBeVisible();
		await expect(page.getByText('Your receipt for this month')).toHaveCount(0);

		await page.getByRole('button', { name: 'Show older' }).click();
		await expect(page.getByText('Your receipt for this month').first()).toBeVisible();
		await expect(page.getByText('Ten shade plants that forgive neglect').first()).toBeVisible();
		// Nothing left: the button goes away rather than offering an empty page.
		await expect(page.getByRole('button', { name: 'Show older' })).toHaveCount(0);
	});

	test('a list that fits on one page offers no "Show older"', async ({ page }) => {
		await page.goto('/');
		await expect(page.getByText('Moving my blog over to Grove?').first()).toBeVisible();
		await expect(page.getByRole('button', { name: 'Show older' })).toHaveCount(0);
	});

	test('Reading appends older issues', async ({ page }) => {
		await page.goto('/reading?scenario=paged');
		await expect(page.getByRole('heading', { name: 'The quiet case for moss' })).toBeVisible();
		await expect(page.getByRole('heading', { name: 'Turning the pile, in four seasons' })).toHaveCount(0);

		await page.getByRole('button', { name: 'Show older' }).click();
		await expect(page.getByRole('heading', { name: 'Turning the pile, in four seasons' })).toBeVisible();
	});

	test('People appends the next hundred', async ({ page }) => {
		await page.goto('/people?scenario=paged');
		await expect(page.getByText('Mara Linden').first()).toBeVisible();
		await expect(page.getByText('Taro Kimura')).toHaveCount(0);

		await page.getByRole('button', { name: 'Show more people' }).click();
		await expect(page.getByText('Taro Kimura').first()).toBeVisible();
		await expect(page.getByRole('button', { name: 'Show more people' })).toHaveCount(0);
	});
});
