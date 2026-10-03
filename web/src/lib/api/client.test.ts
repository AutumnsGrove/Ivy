import { afterEach, describe, expect, it, vi } from 'vitest';
import { api } from './client';
import * as mock from './mock';

/** A fetch reply with just what the transport reads, so the tests need no DOM. */
const reply = (body: unknown, status = 200) => ({ ok: status < 400, status, json: async () => body });

/** Stubs fetch with a table keyed by the request path (query included), returning the body. */
function route(table: Record<string, unknown>) {
	const calls: string[] = [];
	vi.stubGlobal('fetch', async (url: string) => {
		calls.push(url);
		if (!(url in table)) throw new TypeError(`unexpected request to ${url}`);
		const value = table[url];
		if (value instanceof Error) throw value;
		return reply(value);
	});
	return calls;
}

afterEach(() => vi.unstubAllGlobals());

describe('reader api (gateway backed)', () => {
	it('lists accounts from the contract', async () => {
		route({ '/api/v1/accounts': mock.accounts });
		await expect(api.listAccounts()).resolves.toEqual(mock.accounts);
	});

	it('lists the combined inbox and narrows it per account', async () => {
		route({ '/api/v1/inbox': { items: mock.inbox, needCount: 2, unreadCount: 2, readingWaiting: 0 } });
		const all = await api.listInbox();
		expect(all.items.length).toBeGreaterThan(3);

		const calls = route({
			'/api/v1/inbox?account_id=a1': { items: mock.inbox.filter((m) => m.accountId === 'a1'), needCount: 0, unreadCount: 0, readingWaiting: 0 }
		});
		const one = await api.listInbox({ accountId: 'a1' });
		expect(one.items.every((m) => m.accountId === 'a1')).toBe(true);
		expect(calls[0]).toBe('/api/v1/inbox?account_id=a1');
	});

	it('follows the cursor the server hands back', async () => {
		route({
			'/api/v1/inbox': {
				items: mock.inbox,
				needCount: 0,
				unreadCount: 0,
				readingWaiting: 0,
				nextCursor: 'page-2'
			}
		});
		expect((await api.listInbox()).nextCursor).toBe('page-2');
	});

	it('fetches a message and its summary by id', async () => {
		route({
			'/api/v1/messages/m1': { ...mock.inbox[0], ...mock.messageBody('m1') },
			'/api/v1/messages/m1/summary': mock.inbox[0]
		});
		expect((await api.getMessage('m1')).subject).toBe(mock.inbox[0].subject);
		expect((await api.getSummary('m1')).id).toBe('m1');
	});

	it('keeps the stable not_found code a missing message returns', async () => {
		vi.stubGlobal('fetch', async () => reply({ code: 'not_found', message: 'No such message' }, 404));
		await expect(api.getMessage('nope')).rejects.toMatchObject({ code: 'not_found' });
	});

	it('reads mirror health from the gateway', async () => {
		route({
			'/api/v1/mirror/health': {
				accounts: mock.healthAccounts,
				searchIndex: 'Not built yet',
				meaningSearch: 'Not built yet',
				storage: '1.8 GB'
			}
		});
		const health = await api.getHealth();
		expect(health.storage).toBe('1.8 GB');
	});
});

describe('account customization', () => {
	it('renames an account and sets its icon with a PATCH', async () => {
		const calls: { url: string; init: RequestInit }[] = [];
		vi.stubGlobal('fetch', async (url: string, init: RequestInit) => {
			calls.push({ url, init });
			return reply({ ...mock.accounts[0], name: 'Autumn', icon: '🌿' });
		});
		const updated = await api.updateAccountProfile('a1', { displayName: 'Autumn', icon: '🌿' });
		expect(updated.name).toBe('Autumn');
		expect(calls[0].url).toBe('/api/v1/accounts/a1');
		expect(calls[0].init.method).toBe('PATCH');
		expect(JSON.parse(calls[0].init.body as string)).toEqual({ displayName: 'Autumn', icon: '🌿' });
	});

	it('uploads and clears a photo without ever leaving the client module', async () => {
		const calls: { url: string; init: RequestInit }[] = [];
		vi.stubGlobal('fetch', async (url: string, init: RequestInit) => {
			calls.push({ url, init });
			return reply({ ...mock.accounts[0], photo: true });
		});
		const file = new Blob(['png'], { type: 'image/png' });
		await api.setAccountPhoto('a1', file);
		expect(calls[0].url).toBe('/api/v1/accounts/a1/photo');
		expect(calls[0].init.method).toBe('PUT');
		expect((calls[0].init.body as ArrayBuffer).byteLength).toBe(3);

		await api.clearAccountPhoto('a1');
		expect(calls[1].url).toBe('/api/v1/accounts/a1/photo');
		expect(calls[1].init.method).toBe('DELETE');
	});

	it('keeps the stable code the server rejects a bad photo with', async () => {
		vi.stubGlobal('fetch', async () => reply({ code: 'too_large', message: 'That photo is too big' }, 413));
		await expect(api.setAccountPhoto('a1', new Blob(['x']))).rejects.toMatchObject({ code: 'too_large' });
	});
});

describe('still-mocked routes', () => {
	it('searches case-insensitively and reports a total', async () => {
		const found = await api.search('DOMAIN renewal');
		expect(found.total).toBe(found.hits.length);
		expect(found.hits.length).toBeGreaterThan(0);
	});

	it('returns no hits for a query nothing matches', async () => {
		expect(await api.search('xylophone invoice')).toMatchObject({ total: 0, hits: [] });
	});

	it('resolves an ask with cited sources that exist in the answer', async () => {
		const a = await api.ask('When does my domain renew?');
		expect(a.sources.length).toBeGreaterThan(0);
		expect(a.answer.join(' ')).toContain('[1]');
	});

	it('refuses to ask in the limit scenario with a stable code', async () => {
		await expect(api.ask('anything', { scenario: 'limit' })).rejects.toMatchObject({ code: 'ask_limit' });
		await expect(api.ask('anything', { scenario: 'provider-down' })).rejects.toMatchObject({
			code: 'provider_error'
		});
	});

	it('finds a rule by id for the editor', async () => {
		const [first] = await api.listRules();
		expect((await api.getRule(first.id)).id).toBe(first.id);
		await expect(api.getRule('nope')).rejects.toMatchObject({ code: 'not_found' });
	});
});
