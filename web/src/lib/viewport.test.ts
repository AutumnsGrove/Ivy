import { afterEach, describe, expect, it, vi } from 'vitest';

type Listener = (e: { matches: boolean }) => void;

function stubMatchMedia(initial: boolean) {
	let matches = initial;
	const listeners = new Set<Listener>();
	vi.stubGlobal('matchMedia', (query: string) => ({
		get matches() {
			return matches;
		},
		media: query,
		addEventListener: (_: string, l: Listener) => listeners.add(l),
		removeEventListener: (_: string, l: Listener) => listeners.delete(l)
	}));
	return (next: boolean) => {
		matches = next;
		listeners.forEach((l) => l({ matches }));
	};
}

afterEach(() => {
	vi.unstubAllGlobals();
	vi.resetModules();
});

describe('viewport', () => {
	it('starts as phone below the desktop breakpoint', async () => {
		stubMatchMedia(false);
		const { viewport } = await import('./viewport.svelte');
		viewport.start();
		expect(viewport.isDesktop).toBe(false);
	});

	it('flips to desktop when the window widens, and back', async () => {
		const set = stubMatchMedia(false);
		const { viewport } = await import('./viewport.svelte');
		viewport.start();
		set(true);
		expect(viewport.isDesktop).toBe(true);
		set(false);
		expect(viewport.isDesktop).toBe(false);
	});

	it('stops listening when told to', async () => {
		const set = stubMatchMedia(false);
		const { viewport } = await import('./viewport.svelte');
		const stop = viewport.start();
		stop();
		set(true);
		expect(viewport.isDesktop).toBe(false);
	});
});
