import { afterEach, describe, expect, it, vi } from 'vitest';
import { newId } from './ids';

const UUID_V4 = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

describe('newId', () => {
	afterEach(() => vi.unstubAllGlobals());

	it('returns a v4 UUID', () => {
		expect(newId()).toMatch(UUID_V4);
	});

	// Ivy is served over plain http on the tailnet, which is not a secure context, so
	// browsers leave crypto.randomUUID undefined there while getRandomValues still works.
	it('still works when crypto.randomUUID is unavailable', () => {
		vi.stubGlobal('crypto', { getRandomValues: crypto.getRandomValues.bind(crypto) });
		const a = newId();
		const b = newId();
		expect(a).toMatch(UUID_V4);
		expect(b).toMatch(UUID_V4);
		expect(a).not.toBe(b);
	});
});
