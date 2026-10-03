import { expect, test } from './api';
import { ROUTES } from './routes';

for (const [name, path] of Object.entries(ROUTES)) {
	test(`${name} renders cleanly`, async ({ page }, info) => {
		const problems: string[] = [];
		page.on('pageerror', (e) => problems.push(`pageerror: ${e.message}`));
		page.on('console', (m) => {
			if (m.type() !== 'error') return;
			// The not-found screen is reached through a real 404, which the browser always logs.
			if (name === 'not-found' && /status of 404/.test(m.text())) return;
			// The designed failure screens answer with a request the browser logs: an
			// aborted fetch for offline and a 502 for the failed body. That is the
			// state under test, not a defect.
			if ((name === 'offline' || name === 'message-fetch-error') && /Failed to load resource/.test(m.text())) return;
			// The reader's body frame is deliberately script-less (sandbox without
			// allow-scripts); the script WebKit blocks here is Playwright's own
			// frame instrumentation, not page content.
			if (/Blocked script execution/.test(m.text())) return;
			problems.push(`console: ${m.text()}`);
		});

		await page.goto(path);
		await page.waitForLoadState('networkidle');
		// Let entrance transitions and the web fonts settle before the picture is taken.
		await page.evaluate(() => document.fonts.ready);
		await page.screenshot({ path: `shots/${info.project.name}/${name}.png`, fullPage: true });

		expect(problems).toEqual([]);
	});
}
