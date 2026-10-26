import { afterEach, describe, expect, it, vi } from 'vitest';
import { api } from './client';

const reply = (body: unknown, status = 200) => ({ ok: status < 400, status, json: async () => body });

afterEach(() => vi.unstubAllGlobals());

function stubFetch(value: unknown) {
	const calls: { url: string; init: RequestInit }[] = [];
	vi.stubGlobal('fetch', async (url: string, init: RequestInit) => {
		calls.push({ url, init });
		return value;
	});
	return calls;
}

describe('waiting-on api', () => {
	it('lists by account', async () => {
		const page = { items: [{ id: 's1', accountId: 'a1', to: ['Sam'], subject: 'Figures', sentAt: '2026-10-20T09:00:00Z', days: 5 }] };
		const calls = stubFetch(reply(page));
		await expect(api.listWaitingOn({ accountId: 'a1' })).resolves.toEqual(page);
		expect(calls[0].url).toBe('/api/v1/waiting?account_id=a1');
	});

	it('lists every account when none is named', async () => {
		const calls = stubFetch(reply({ items: [] }));
		await api.listWaitingOn();
		expect(calls[0].url).toBe('/api/v1/waiting');
	});

	it('stops one message by escaped id and treats the empty 204 as done', async () => {
		const calls = stubFetch({ ok: true, status: 204, json: async () => Promise.reject(new Error('no body')) });
		await expect(api.stopWaiting('a/b')).resolves.toBeUndefined();
		expect(calls[0].url).toBe('/api/v1/waiting/a%2Fb/stop');
		expect(calls[0].init.method).toBe('POST');
	});
});
