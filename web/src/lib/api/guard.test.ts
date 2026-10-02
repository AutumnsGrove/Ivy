import { describe, expect, it } from 'vitest';
import { ApiError } from './client';
import { guard } from './guard';

const status = async (p: Promise<unknown>) => {
	try {
		await p;
	} catch (e) {
		return e as { status: number; body: { message: string; code?: string } };
	}
	throw new Error('expected a rejection');
};

describe('guard', () => {
	it('passes values through', async () => {
		expect(await guard(Promise.resolve(7))).toBe(7);
	});

	it.each([
		['offline', 503],
		['not_found', 404],
		['fetch_failed', 502],
		['provider_error', 502],
		['ask_limit', 429]
	] as const)('maps %s to HTTP %i and keeps the code for the UI', async (code, http) => {
		const err = await status(guard(Promise.reject(new ApiError(code, 'x'))));
		expect(err.status).toBe(http);
		expect(err.body.code).toBe(code);
	});

	it('rethrows unknown errors untouched', async () => {
		await expect(guard(Promise.reject(new TypeError('boom')))).rejects.toThrow('boom');
	});
});
