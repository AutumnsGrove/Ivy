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

describe('identities api', () => {
	it('lists an account\'s send addresses', async () => {
		const calls = stubFetch(reply({ identities: [] }));
		await expect(api.listIdentities('a1')).resolves.toEqual({ identities: [] });
		expect(calls[0].url).toBe('/api/v1/accounts/a1/identities');
	});

	it('saves an identity by address as a JSON body', async () => {
		const calls = stubFetch(reply({ id: 'i1', accountId: 'a1', address: 'hello@example.com', name: 'Autumn', signature: '', primary: false }));
		await api.saveIdentity('a1', { address: 'hello@example.com', name: 'Autumn' });
		expect(calls[0].url).toBe('/api/v1/accounts/a1/identities');
		expect(calls[0].init.method).toBe('PUT');
		expect(JSON.parse(calls[0].init.body as string)).toEqual({ address: 'hello@example.com', name: 'Autumn' });
	});

	it('deletes an identity by row id, escaping it', async () => {
		const calls = stubFetch({ ok: true, status: 204, json: async () => Promise.reject(new SyntaxError('empty')) });
		await expect(api.deleteIdentity('a1', 'i/1')).resolves.toBeUndefined();
		expect(calls[0].url).toBe('/api/v1/accounts/a1/identities/i%2F1');
		expect(calls[0].init.method).toBe('DELETE');
	});

	it('keeps the identity limit refusal so the editor can say so', async () => {
		stubFetch(reply({ code: 'too_many_identities', message: 'There are too many sending addresses' }, 409));
		await expect(api.saveIdentity('a1', { address: 'x@example.com' })).rejects.toMatchObject({ code: 'too_many_identities' });
	});

	it('asks for a reply prefill, with all as a query flag', async () => {
		const calls = stubFetch(reply({ from: 'me@example.com', to: [], cc: [], subject: '', text: '', references: [] }));
		await api.replyPrefill('m1', true);
		expect(calls[0].url).toBe('/api/v1/messages/m1/reply?all=true');
	});

	it('asks for a forward prefill', async () => {
		const calls = stubFetch(reply({ from: 'me@example.com', to: [], cc: [], subject: '', text: '', references: [] }));
		await api.forwardPrefill('m1');
		expect(calls[0].url).toBe('/api/v1/messages/m1/forward');
	});
});
