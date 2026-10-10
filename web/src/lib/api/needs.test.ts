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

describe('needs-attention api', () => {
	it('pages the view by account and cursor', async () => {
		const page = { items: [], needCount: 0, unreadCount: 0, readingWaiting: 0 };
		const calls = stubFetch(reply(page));
		await expect(api.listNeedsAttention({ accountId: 'a1', cursor: 'c2' })).resolves.toEqual(page);
		expect(calls[0].url).toBe('/api/v1/needs-attention?account_id=a1&cursor=c2');
	});

	it('dismisses one message by escaped id and treats the empty 204 as done', async () => {
		const calls = stubFetch({ ok: true, status: 204, json: async () => Promise.reject(new Error('no body')) });
		await expect(api.dismissNeedsAttention('a/b')).resolves.toBeUndefined();
		expect(calls[0].url).toBe('/api/v1/messages/a%2Fb/needs/dismiss');
		expect(calls[0].init.method).toBe('POST');
	});
});
