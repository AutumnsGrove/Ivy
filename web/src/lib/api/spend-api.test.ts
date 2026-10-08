import { afterEach, describe, expect, it, vi } from 'vitest';
import { api } from './client';

// Spend is a real endpoint now: these pin the requests the client makes and how it
// reads a failure. The numbers behind them are summed by the server (Go tests).
function respond(status: number, body: unknown) {
	const seen: string[] = [];
	vi.stubGlobal('fetch', async (url: string) => {
		seen.push(url);
		return { ok: status < 400, status, json: async () => body };
	});
	return seen;
}
afterEach(() => vi.unstubAllGlobals());

describe('spend api', () => {
	const now = new Date('2026-10-15T14:00:00Z');

	it('asks for a window by its start, and for all time with none', async () => {
		const seen = respond(200, { totalUsd: 0 });
		await api.getSpend('7d', now);
		await api.getSpend('all', now);
		expect(seen[0]).toBe(`/api/v1/spend?from=${encodeURIComponent('2026-10-08T14:00:00.000Z')}`);
		expect(seen[1]).toBe('/api/v1/spend');
	});

	it('pages the log with the cursor and filter it was given', async () => {
		const seen = respond(200, { items: [] });
		await api.listCalls();
		await api.listCalls({ outcome: 'error', cursor: '41', limit: 10 });
		expect(seen[0]).toBe('/api/v1/spend/calls');
		expect(seen[1]).toBe('/api/v1/spend/calls?outcome=error&cursor=41&limit=10');
	});

	it('reads the server’s refusal of a filter as bad_request', async () => {
		respond(400, { code: 'bad_request', message: 'That filter is not valid' });
		await expect(api.listCalls({ cursor: 'forged' })).rejects.toMatchObject({ code: 'bad_request' });
	});

	it('builds the download link for the filter on screen', () => {
		expect(api.callsExportUrl('csv')).toBe('/api/v1/spend/calls/export?format=csv');
		expect(api.callsExportUrl('csv', { outcome: 'refused' })).toBe('/api/v1/spend/calls/export?format=csv&outcome=refused');
	});
});
