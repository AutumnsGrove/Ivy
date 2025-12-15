import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vitest/config';

export default defineConfig({
	plugins: [sveltekit()],
	test: {
		include: ['tests/**/*.{test,spec}.{js,ts}', 'src/**/*.{test,spec}.{js,ts}'],
		globals: true,
		environment: 'node',
		setupFiles: ['./tests/setup.ts'],
		coverage: {
			provider: 'v8',
			reporter: ['text', 'html', 'lcov'],
			exclude: [
				'node_modules/',
				'tests/',
				'**/*.d.ts',
				'**/*.config.*',
				'src/routes/**/*.svelte',
				'.svelte-kit/'
			],
			include: ['src/lib/**/*.ts', 'src/routes/api/**/*.ts', 'src/workers/**/*.ts'],
			thresholds: {
				lines: 70,
				functions: 70,
				branches: 60,
				statements: 70
			}
		},
		alias: {
			$lib: '/src/lib'
		}
	}
});
