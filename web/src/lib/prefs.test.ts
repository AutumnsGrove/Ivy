import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

let dark = true;
const listeners = new Set<() => void>();

beforeEach(() => {
	dark = true;
	listeners.clear();
	localStorage.clear();
	document.documentElement.removeAttribute('data-theme');
	document.documentElement.removeAttribute('data-accent');
	document.documentElement.removeAttribute('data-motion');
	vi.stubGlobal('matchMedia', (q: string) => ({
		get matches() {
			return q.includes('dark') ? dark : false;
		},
		addEventListener: (_: string, l: () => void) => listeners.add(l),
		removeEventListener: (_: string, l: () => void) => listeners.delete(l)
	}));
});
afterEach(() => {
	vi.unstubAllGlobals();
	vi.resetModules();
});

const load = async () => (await import('./prefs.svelte')).prefs;

describe('prefs', () => {
	it('defaults to the night garden with the lilac accent and gentle motion', async () => {
		const prefs = await load();
		prefs.apply();
		const root = document.documentElement;
		expect(root.dataset.theme).toBe('night');
		expect(root.dataset.accent).toBe('lilac');
		expect(root.dataset.motion).toBe('gentle');
	});

	it('applies a chosen theme and remembers it across loads', async () => {
		let prefs = await load();
		prefs.set('theme', 'day');
		expect(document.documentElement.dataset.theme).toBe('day');

		vi.resetModules();
		prefs = await load();
		prefs.apply();
		expect(prefs.theme).toBe('day');
		expect(document.documentElement.dataset.theme).toBe('day');
	});

	it('follows the system when set to auto, including later changes', async () => {
		const prefs = await load();
		prefs.set('theme', 'auto');
		expect(document.documentElement.dataset.theme).toBe('night');

		dark = false;
		listeners.forEach((l) => l());
		expect(document.documentElement.dataset.theme).toBe('day');
	});

	it('ignores corrupt stored values instead of breaking the page', async () => {
		localStorage.setItem('ivy.prefs', '{"theme":"neon","accent":42');
		const prefs = await load();
		prefs.apply();
		expect(document.documentElement.dataset.theme).toBe('night');
	});
});
