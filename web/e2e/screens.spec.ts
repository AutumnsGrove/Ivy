import { expect, test } from '@playwright/test';
import { ROUTES } from './routes';

for (const [name, path] of Object.entries(ROUTES)) {
	test(`${name} renders cleanly`, async ({ page }, info) => {
		const problems: string[] = [];
		page.on('pageerror', (e) => problems.push(`pageerror: ${e.message}`));
		page.on('console', (m) => m.type() === 'error' && problems.push(`console: ${m.text()}`));

		await page.goto(path);
		await page.waitForLoadState('networkidle');
		// Let entrance transitions and the web fonts settle before the picture is taken.
		await page.evaluate(() => document.fonts.ready);
		await page.screenshot({ path: `shots/${info.project.name}/${name}.png`, fullPage: true });

		expect(problems).toEqual([]);
	});
}
