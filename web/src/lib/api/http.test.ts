import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from './errors';
import { apiPath, request } from './http';

/** A fetch reply with just the fields the transport reads, so the test needs no DOM. */
function reply(status: number, body: unknown, ok = status >= 200 && status < 300) {
	return { ok, status, json: async () => body };
}

const stub = (value: unknown) => {
	const mock = vi.fn(async (_url: string, _init?: RequestInit) => value);
	vi.stubGlobal('fetch', mock);
	return mock;
};

afterEach(() => vi.unstubAllGlobals());

describe('apiPath', () => {
	it('joins only the query values that are present', () => {
		expect(apiPath('/inbox')).toBe('/inbox');
		expect(apiPath('/inbox', { account_id: 'a1' })).toBe('/inbox?account_id=a1');
		expect(apiPath('/inbox', { account_id: undefined, cursor: 'x' })).toBe('/inbox?cursor=x');
	});

	it('escapes query values, because ids come from the URL', () => {
		expect(apiPath('/inbox', { cursor: 'a b/c' })).toBe('/inbox?cursor=a+b%2Fc');
	});
});

describe('request', () => {
	it('asks the versioned path and returns the parsed body', async () => {
		const mock = stub(reply(200, { id: 'm1' }));
		await expect(request('/messages/m1')).resolves.toEqual({ id: 'm1' });
		expect(mock).toHaveBeenCalledWith('/api/v1/messages/m1', expect.anything());
	});

	it('sends Accept: application/json', async () => {
		const mock = stub(reply(200, {}));
		await request('/inbox');
		const init = mock.mock.calls[0][1] as RequestInit;
		expect(init.headers).toMatchObject({ Accept: 'application/json' });
	});

	it('maps the server error envelope onto its stable code', async () => {
		stub(reply(404, { code: 'not_found', message: 'No such message' }));
		await expect(request('/messages/nope')).rejects.toMatchObject({
			code: 'not_found',
			message: 'No such message'
		});
	});

	it('honours the ask cap and provider codes the UI already knows', async () => {
		stub(reply(429, { code: 'ask_limit', message: 'resting' }));
		await expect(request('/ask')).rejects.toMatchObject({ code: 'ask_limit' });
		stub(reply(502, { code: 'provider_error', message: 'down' }));
		await expect(request('/ask')).rejects.toMatchObject({ code: 'provider_error' });
	});

	it('treats a 204 as success with nothing to read, not as an unreadable body', async () => {
		const mock = vi.fn(async () => ({
			ok: true,
			status: 204,
			json: async () => {
				throw new SyntaxError('Unexpected end of JSON input');
			}
		}));
		vi.stubGlobal('fetch', mock);
		await expect(request<void>('/tags/t1', { method: 'DELETE' })).resolves.toBeUndefined();
	});

	it('keeps the tag refusal codes the tag screens word themselves', async () => {
		stub(reply(409, { code: 'too_many_tags', message: 'There are too many tags' }));
		await expect(request('/tags', { method: 'POST' })).rejects.toMatchObject({ code: 'too_many_tags' });
		stub(reply(409, { code: 'unknown_tag', message: 'That tag no longer exists' }));
		await expect(request('/outbox', { method: 'POST' })).rejects.toMatchObject({ code: 'unknown_tag' });
	});

	it('treats a body it cannot parse as a server failure, not a success', async () => {
		stub({ ok: false, status: 500, json: async () => { throw new SyntaxError('bad json'); } });
		await expect(request('/inbox')).rejects.toMatchObject({ code: 'internal_error' });
	});

	it('maps a transport failure to the offline code, so the calm screen shows', async () => {
		vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('Failed to fetch'); }));
		await expect(request('/inbox')).rejects.toMatchObject({ code: 'offline' });
	});

	it('throws an ApiError, never a bare error', async () => {
		stub(reply(500, { code: 'internal_error', message: 'boom' }));
		await expect(request('/inbox')).rejects.toBeInstanceOf(ApiError);
	});
});
