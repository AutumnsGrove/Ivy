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

describe('tags api', () => {
	it('lists the tags from the gateway', async () => {
		const calls = stubFetch(reply({ mine: [], placed: [], activeRules: 0 }));
		await expect(api.listTags()).resolves.toEqual({ mine: [], placed: [], activeRules: 0 });
		expect(calls[0].url).toBe('/api/v1/tags');
	});

	it('creates a tag with a JSON body', async () => {
		const calls = stubFetch(reply({ id: 't1', name: 'Work', color: 'sky', count: 0 }, 201));
		const tag = await api.createTag({ name: 'Work', color: 'sky' });
		expect(tag.id).toBe('t1');
		expect(calls[0].url).toBe('/api/v1/tags');
		expect(calls[0].init.method).toBe('POST');
		expect(JSON.parse(calls[0].init.body as string)).toEqual({ name: 'Work', color: 'sky' });
	});

	it('renames and recolours by id, escaping it', async () => {
		const calls = stubFetch(reply({ id: 'a/b', name: 'Bills', color: 'rose', count: 2 }));
		await api.updateTag('a/b', { name: 'Bills' });
		expect(calls[0].url).toBe('/api/v1/tags/a%2Fb');
		expect(calls[0].init.method).toBe('PATCH');
	});

	it('deletes a tag and treats the empty 204 as done', async () => {
		const calls = stubFetch({ ok: true, status: 204, json: async () => Promise.reject(new SyntaxError('empty')) });
		await expect(api.deleteTag('t1')).resolves.toBeUndefined();
		expect(calls[0].init.method).toBe('DELETE');
	});

	it('keeps the limit refusal so the sheet can say so', async () => {
		stubFetch(reply({ code: 'too_many_tags', message: 'There are too many tags' }, 409));
		await expect(api.createTag({ name: 'x' })).rejects.toMatchObject({ code: 'too_many_tags' });
	});
});
