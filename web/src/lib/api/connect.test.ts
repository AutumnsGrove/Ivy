import { afterEach, describe, expect, it, vi } from 'vitest';
import { api } from './client';

const reply = (status: number, body: unknown) => ({ ok: status >= 200 && status < 300, status, json: async () => body });

const stub = (value: unknown) => {
	const mock = vi.fn(async (_url: string, _init?: RequestInit) => value);
	vi.stubGlobal('fetch', mock);
	return mock;
};

afterEach(() => vi.unstubAllGlobals());

describe('connectAccount', () => {
	it('posts the address, password and smart flag as JSON and returns the new id', async () => {
		const mock = stub(reply(201, { id: 'purelymail' }));
		await expect(api.connectAccount('me@grove.test', 'hunter2', true)).resolves.toEqual({ id: 'purelymail' });

		const [url, init] = mock.mock.calls[0] as [string, RequestInit];
		expect(url).toBe('/api/v1/accounts');
		expect(init.method).toBe('POST');
		expect(init.headers).toMatchObject({ 'Content-Type': 'application/json' });
		expect(JSON.parse(init.body as string)).toEqual({ address: 'me@grove.test', password: 'hunter2', smart: true });
	});

	it('never puts the password in the URL', async () => {
		const mock = stub(reply(201, { id: 'purelymail' }));
		await api.connectAccount('me@grove.test', 'hunter2-needle', false);
		expect(mock.mock.calls[0][0]).not.toContain('hunter2-needle');
	});

	it.each([
		[422, 'auth_failed'],
		[502, 'unreachable'],
		[502, 'connect_failed'],
		[409, 'already_connected'],
		[503, 'connect_unavailable']
	])('keeps the %i %s code so the screen can say what happened', async (status, code) => {
		stub(reply(status, { code, message: 'x' }));
		await expect(api.connectAccount('me@grove.test', 'pw', false)).rejects.toMatchObject({ code });
	});
});

describe('updateAccountPassword', () => {
	it('puts the new password to the account and resolves on a 204', async () => {
		const mock = vi.fn(async (_url: string, _init?: RequestInit) => ({ ok: true, status: 204, json: async () => undefined }));
		vi.stubGlobal('fetch', mock);
		await expect(api.updateAccountPassword('purelymail-2', 'newpass')).resolves.toBeUndefined();

		const [url, init] = mock.mock.calls[0] as [string, RequestInit];
		expect(url).toBe('/api/v1/accounts/purelymail-2/password');
		expect(init.method).toBe('PUT');
		expect(JSON.parse(init.body as string)).toEqual({ password: 'newpass' });
	});

	it('escapes the account id, which comes from the URL', async () => {
		const mock = vi.fn(async (_url: string, _init?: RequestInit) => ({ ok: true, status: 204, json: async () => undefined }));
		vi.stubGlobal('fetch', mock);
		await api.updateAccountPassword('a/b', 'pw');
		expect(mock.mock.calls[0][0]).toBe('/api/v1/accounts/a%2Fb/password');
	});

	it('keeps a refused password as auth_failed', async () => {
		stub(reply(422, { code: 'auth_failed', message: 'no' }));
		await expect(api.updateAccountPassword('purelymail', 'bad')).rejects.toMatchObject({ code: 'auth_failed' });
	});
});
