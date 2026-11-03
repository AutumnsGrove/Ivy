// ESLint for the frontend (STANDARDS.md section 5, CI.md section 2). Prettier
// owns whitespace and quotes, so its formatting rules are switched off here;
// what is left is correctness and Svelte's accessibility rules.
import js from '@eslint/js';
import prettier from 'eslint-config-prettier';
import svelte from 'eslint-plugin-svelte';
import globals from 'globals';
import tseslint from 'typescript-eslint';

export default tseslint.config(
	{
		ignores: [
			'build/',
			'.svelte-kit/',
			'node_modules/',
			'test-results/',
			// Generated from api/openapi.yaml by `make generate`.
			'src/lib/api/schema.d.ts'
		]
	},
	js.configs.recommended,
	...tseslint.configs.recommended,
	...svelte.configs.recommended,
	prettier,
	{
		languageOptions: {
			globals: { ...globals.browser, ...globals.node },
			parserOptions: { sourceType: 'module', extraFileExtensions: ['.svelte'] }
		},
		rules: {
			// A leading underscore is how this codebase says "deliberately unused"
			// (a fake fetch signature, a mock's unused argument).
			'@typescript-eslint/no-unused-vars': [
				'error',
				{ argsIgnorePattern: '^_', varsIgnorePattern: '^_', caughtErrorsIgnorePattern: '^_' }
			]
		}
	},
	{
		// TypeScript inside a <script lang="ts"> block, and inside a runes module
		// (`*.svelte.ts`), needs the TS parser; the Svelte config only supplies the
		// Svelte one.
		files: ['**/*.svelte', '**/*.svelte.ts', '**/*.svelte.js'],
		languageOptions: { parserOptions: { parser: tseslint.parser } }
	},
	{
		rules: {
			// The typed-route `resolve()` this asks for exists to prefix every link
			// with `paths.base`. Ivy is served from the root, and most links are
			// built in shared components from an `href` prop, so adopting it would
			// rewrite every call site for a failure that cannot happen here.
			// Revisit if `paths.base` is ever set.
			'svelte/no-navigation-without-resolve': 'off'
		}
	}
);
