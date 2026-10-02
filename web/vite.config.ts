import adapter from '@sveltejs/adapter-static';
import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vitest/config';

export default defineConfig({
	plugins: [
		sveltekit({
			compilerOptions: {
				// Force runes mode for the project, except for libraries. Can be removed in svelte 6.
				runes: ({ filename }) =>
					filename.split(/[/\\]/).includes('node_modules') ? undefined : true
			},
			// The Go binary embeds this output; SPA fallback because routes are all client-rendered.
			adapter: adapter({ fallback: 'index.html', precompress: false, strict: false })
		})
	],
	// ivy-dev sets IVY_API_TARGET so the dev server proxies the API and SSE to
	// the real binary; without it (vitest) no proxy is configured.
	server: process.env.IVY_API_TARGET
		? {
				proxy: {
					'/api': { target: process.env.IVY_API_TARGET, changeOrigin: false }
				}
			}
		: undefined,
	resolve: process.env.VITEST ? { conditions: ['browser'] } : undefined,
	test: {
		environment: 'jsdom',
		include: ['src/**/*.test.ts'],
		setupFiles: ['src/test-setup.ts']
	}
});
