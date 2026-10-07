import AxeBuilder from '@axe-core/playwright';
import { expect, test } from './api';

// Accessibility is part of done (STANDARDS.md section 5, TESTING.md section 1).
// A representative path through each shape of screen runs axe on both
// viewports; a violation fails the run with the rule and the element.
const SCREENS: Record<string, string> = {
	inbox: '/',
	'message': '/m/m1',
	'settings': '/settings',
	'account profile': '/settings/account/a1',
	'mirror health': '/settings/health',
	spend: '/settings/spend',
	'spend, nothing sent': '/settings/spend?scenario=no-spend',
	'spend, cap reached': '/settings/spend?scenario=cap-hit',
	'call log': '/settings/spend/calls',
	search: '/search?q=domain+renewal',
	tags: '/tags',
	reading: '/reading',
	'empty inbox': '/?scenario=empty',
	'sync error': '/?scenario=sync-error',
	offline: '/?scenario=offline',
	'failed body': '/m/m1?scenario=fetch-error'
};

for (const [name, path] of Object.entries(SCREENS)) {
	test(`${name} has no accessibility violations`, async ({ page }) => {
		await page.goto(path);
		await page.waitForLoadState('networkidle');
		const results = await new AxeBuilder({ page })
			// The reader's body frame is a sandboxed document without allow-scripts,
			// so axe cannot inject into it; its markup is server-sanitised and covered
			// by the Go tests and the remote-content spec.
			.exclude('iframe')
			.analyze();
		const summary = results.violations.map((v) => ({
			id: v.id,
			impact: v.impact,
			targets: v.nodes.map((n) => n.target.join(' '))
		}));
		expect(summary).toEqual([]);
	});
}

// Issue #11: the chooser is its own state of the inbox, with checkboxes and a toolbar.
test('choosing several messages has no accessibility violations', async ({ page }) => {
	await page.goto('/');
	await page.getByRole('button', { name: 'Select', exact: true }).click();
	await page.getByRole('checkbox').first().click();
	await page.getByRole('toolbar', { name: 'Selection' }).getByRole('button', { name: 'More' }).click();
	await page.getByRole('dialog', { name: 'More actions for the selection' }).waitFor();
	const results = await new AxeBuilder({ page }).exclude('iframe').analyze();
	expect(results.violations.map((v) => ({ id: v.id, targets: v.nodes.map((n) => n.target.join(' ')) }))).toEqual([]);
});
