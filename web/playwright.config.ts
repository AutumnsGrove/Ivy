import { defineConfig, devices } from '@playwright/test';

// Phone is the primary device (Safari on iPhone), so it runs on WebKit; desktop on Chromium.
const PORT = 4173;

export default defineConfig({
	testDir: 'e2e',
	fullyParallel: true,
	reporter: 'list',
	use: { baseURL: `http://localhost:${PORT}`, trace: 'retain-on-failure' },
	webServer: {
		command: `pnpm exec vite dev --port ${PORT} --strictPort`,
		url: `http://localhost:${PORT}`,
		reuseExistingServer: true,
		timeout: 120_000
	},
	projects: [
		{ name: 'phone', use: { ...devices['iPhone 14'] } },
		{ name: 'desktop', use: { browserName: 'chromium', viewport: { width: 1440, height: 860 } } }
	]
});
