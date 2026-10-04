import { afterEach, describe, expect, it, vi } from 'vitest';
import { api } from './client';

/** A fetch reply with just what the transport reads. */
const reply = (body: unknown, status = 200) => ({ ok: status < 400, status, json: async () => body });

afterEach(() => vi.unstubAllGlobals());

describe('outbox api', () => {
	it('posts the action as JSON and returns the op', async () => {
		const calls: RequestInit[] = [];
		vi.stubGlobal('fetch', async (_url: string, init: RequestInit) => {
			calls.push(init);
			return reply(
				{
					id: 'op-1',
					accountId: 'a1',
					messageId: 'm1',
					kind: 'move',
					state: 'pending',
					attempts: 0,
					createdAt: '2026-10-04T00:00:00Z',
					updatedAt: '2026-10-04T00:00:00Z'
				},
				202
			);
		});
		const op = await api.enqueueAction({ messageId: 'm1', action: 'archive' });
		expect(op.id).toBe('op-1');
		expect(calls[0].method).toBe('POST');
		expect(JSON.parse(calls[0].body as string)).toEqual({ messageId: 'm1', action: 'archive' });
	});

	it('keeps a stable refusal code from the server', async () => {
		vi.stubGlobal('fetch', async () =>
			reply({ code: 'no_archive_folder', message: 'This account has no Archive folder' }, 409)
		);
		await expect(api.enqueueAction({ messageId: 'm1', action: 'archive' })).rejects.toMatchObject({
			code: 'no_archive_folder'
		});
	});

	it('lists the overlay and the history', async () => {
		const calls: string[] = [];
		vi.stubGlobal('fetch', async (url: string) => {
			calls.push(url);
			return reply({ active: [], recent: [] });
		});
		await api.listOutbox('a1');
		expect(calls[0]).toBe('/api/v1/outbox?account_id=a1');
	});

	it('retries and dismisses by id', async () => {
		const calls: { url: string; method?: string }[] = [];
		vi.stubGlobal('fetch', async (url: string, init: RequestInit = {}) => {
			calls.push({ url, method: init.method });
			return reply({ id: 'op-1', accountId: 'a1', messageId: 'm1', kind: 'flags', state: 'pending', attempts: 0, createdAt: '', updatedAt: '' });
		});
		await api.retryOutbox('op-1');
		expect(calls[0]).toEqual({ url: '/api/v1/outbox/op-1/retry', method: 'POST' });
		await api.dismissOutbox('op-1');
		expect(calls[1]).toEqual({ url: '/api/v1/outbox/op-1', method: 'DELETE' });
	});
});
