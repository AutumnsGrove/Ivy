import { defineConfig, devices } from '@playwright/test';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';

// The day-one smoke slice (TESTING.md 0): the *real* Go binary, serving the
// embedded, precompressed frontend, with mailworld behind it. Unlike
// playwright.config.ts (Vite + mocks), this proves the production artefact
// boots and renders on both viewports. Run `make web-assets` first: the binary
// embeds internal/webui/build/, which a fresh checkout does not contain.
// Keep in step with config.DefaultListen. IVY_SMOKE_PORT moves it if something else holds it.
const PORT = Number(process.env.IVY_SMOKE_PORT ?? 8418);
const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');

export default defineConfig({
	testDir: 'e2e',
	testMatch: 'smoke.spec.ts',
	fullyParallel: false,
	reporter: process.env.CI ? [['github'], ['list']] : 'list',
	use: {
		baseURL: `http://127.0.0.1:${PORT}`,
		trace: 'retain-on-failure',
		screenshot: 'only-on-failure'
	},
	webServer: {
		// Default `--watch` supervises the compiled Ivy binary (not an in-process
		// gateway), which is exactly what production runs.
		command: `go run ./cmd/ivy-dev up --no-web --profile minimal --mode full --llm fake --listen 127.0.0.1:${PORT}`,
		cwd: repoRoot,
		url: `http://127.0.0.1:${PORT}/api/v1/health`,
		reuseExistingServer: !process.env.CI,
		timeout: 180_000,
		stdout: 'pipe',
		stderr: 'pipe'
	},
	projects: [
		{ name: 'phone', use: { ...devices['iPhone 14'] } },
		{ name: 'desktop', use: { browserName: 'chromium', viewport: { width: 1440, height: 860 } } }
	]
});
