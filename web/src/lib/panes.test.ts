import { beforeEach, describe, expect, it } from 'vitest';
import { clampPane, MIN_READER } from './panes.svelte';

describe('clampPane', () => {
	it('keeps the accounts pane between its minimum and maximum', () => {
		expect(clampPane('nav', 50, 1400, 420)).toBe(180);
		expect(clampPane('nav', 900, 1400, 420)).toBe(360);
		expect(clampPane('nav', 250, 1400, 420)).toBe(250);
	});

	it('keeps the list pane between its minimum and maximum', () => {
		expect(clampPane('list', 100, 1400, 236)).toBe(300);
		expect(clampPane('list', 2000, 2400, 236)).toBe(640);
	});

	it('never lets the message pane shrink below its readable minimum', () => {
		const total = 1000;
		const nav = 236;
		const list = clampPane('list', 640, total, nav);
		expect(total - nav - list).toBeGreaterThanOrEqual(MIN_READER);
	});

	it('still respects the pane minimum on a window too small for everything', () => {
		expect(clampPane('list', 500, 500, 236)).toBe(300);
	});
});

describe('prefs persistence', () => {
	beforeEach(() => localStorage.clear());

	it('remembers sizes between visits and ignores corrupt storage', async () => {
		const { panes } = await import('./panes.svelte');
		panes.set('nav', 300, 1400);
		expect(panes.nav).toBe(300);

		localStorage.setItem('ivy.panes', 'not json');
		const { loadPanes } = await import('./panes.svelte');
		expect(loadPanes()).toEqual({ nav: 236, list: 420 });
	});

	it('resets one pane to its default', async () => {
		const { panes } = await import('./panes.svelte');
		panes.set('list', 520, 1600);
		panes.reset('list');
		expect(panes.list).toBe(420);
	});
});
