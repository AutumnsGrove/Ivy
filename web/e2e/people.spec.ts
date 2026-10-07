import { expect, test } from './api';

// Issue #12: a name with no space is the whole string, and it used to run over
// the next avatar in Often in touch. Every label must stay inside its own item.
test('Often in touch keeps each name inside its own avatar column', async ({ page }) => {
	await page.goto('/people?scenario=hostile-names');
	const items = page.locator('.often a');
	await expect(items).toHaveCount(5);

	const boxes = [];
	for (let i = 0; i < 5; i++) {
		const item = items.nth(i);
		const link = (await item.boundingBox())!;
		const label = (await item.locator('.label').boundingBox())!;
		expect(label.x, `label ${i} starts inside its item`).toBeGreaterThanOrEqual(link.x - 1);
		expect(label.x + label.width, `label ${i} ends inside its item`).toBeLessThanOrEqual(link.x + link.width + 1);
		boxes.push(link);
	}
	for (let i = 1; i < boxes.length; i++) {
		expect(boxes[i].x, `item ${i} starts after item ${i - 1} ends`).toBeGreaterThanOrEqual(boxes[i - 1].x + boxes[i - 1].width - 1);
	}
});

// Issue #13: People is a way to search your mail, not a kind of tag.
test('People is reached from the Search switch, not from Tags', async ({ page }) => {
	await page.goto('/tags');
	await expect(page.getByRole('link', { name: 'People' })).toHaveCount(0);

	await page.goto('/search');
	await page.getByRole('link', { name: 'People' }).click();
	await expect(page).toHaveURL(/\/people$/);
	await expect(page.getByRole('link', { name: 'Search', exact: true }).first()).toBeVisible();
	await expect(page.getByPlaceholder('Find someone')).toBeVisible();
});

test('the full name is still reachable in the list below', async ({ page }) => {
	await page.goto('/people?scenario=hostile-names');
	await expect(page.locator('.row').getByText('AutumnsGrove/Lattice')).toBeVisible();
});
