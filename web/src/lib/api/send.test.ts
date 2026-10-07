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

const sendStatus = {
	id: 's1',
	accountId: 'a1',
	state: 'queued',
	to: ['mara@example.com'],
	createdAt: '2026-10-06T09:00:00Z',
	updatedAt: '2026-10-06T09:00:00Z'
};

describe('send api', () => {
	it('queues a message with the whole request as JSON', async () => {
		const calls = stubFetch(reply(sendStatus, 202));
		const st = await api.sendMessage({
			accountId: 'a1',
			from: 'hello@example.com',
			to: ['mara@example.com'],
			subject: 'Hi',
			text: 'Hello',
			markdown: true
		});
		expect(st.state).toBe('queued');
		expect(calls[0].url).toBe('/api/v1/send');
		expect(calls[0].init.method).toBe('POST');
		expect(JSON.parse(calls[0].init.body as string)).toMatchObject({
			accountId: 'a1',
			from: 'hello@example.com',
			to: ['mara@example.com'],
			subject: 'Hi',
			text: 'Hello',
			markdown: true
		});
	});

	it('reads one send and the account list', async () => {
		const calls = stubFetch(reply(sendStatus));
		expect((await api.getSend('s1')).id).toBe('s1');
		expect(calls[0].url).toBe('/api/v1/send/s1');

		const scoped = stubFetch(reply({ active: [], recent: [] }));
		await api.listSends('a1');
		expect(scoped[0].url).toBe('/api/v1/send?account_id=a1');
	});

	it('undoes a queued send', async () => {
		const calls = stubFetch(reply({ ...sendStatus, state: 'cancelled', draft: '{"to":["mara@example.com"]}' }));
		const cancelled = await api.undoSend('s1');
		expect(cancelled.state).toBe('cancelled');
		expect(calls[0].url).toBe('/api/v1/send/s1/undo');
		expect(calls[0].init.method).toBe('POST');
	});

	it('keeps the queue-full refusal so the screen can say wait', async () => {
		stubFetch(reply({ code: 'send_full', message: 'There are too many unsent messages' }, 409));
		await expect(api.sendMessage({ accountId: 'a1', from: 'x@y', to: [], subject: '', text: '' })).rejects.toMatchObject({
			code: 'send_full'
		});
	});

	it('keeps too_late on an undo after the window', async () => {
		stubFetch(reply({ code: 'too_late', message: 'This message can no longer be undone' }, 409));
		await expect(api.undoSend('s1')).rejects.toMatchObject({ code: 'too_late' });
	});

	it('keeps invalid_message from the builder', async () => {
		stubFetch(reply({ code: 'invalid_message', message: 'Ivy cannot send that message: the subject' }, 400));
		await expect(api.sendMessage({ accountId: 'a1', from: 'x@y', to: [], subject: '', text: '' })).rejects.toMatchObject({
			code: 'invalid_message'
		});
	});
});

describe('drafts api', () => {
	it('lists drafts for an account', async () => {
		const calls = stubFetch(reply({ drafts: [] }));
		await api.listDrafts('a1');
		expect(calls[0].url).toBe('/api/v1/drafts?account_id=a1');
	});

	it('saves a draft as JSON and returns the version', async () => {
		const calls = stubFetch(
			reply({ id: 'd1', draftId: 'draft-1', accountId: 'a1', version: 1, subject: 'Hi', to: [], updatedAt: '', source: 'local' })
		);
		const saved = await api.saveDraft({ accountId: 'a1', from: 'hello@example.com', to: [], text: 'Hi' });
		expect(saved.version).toBe(1);
		expect(calls[0].url).toBe('/api/v1/drafts');
		expect(calls[0].init.method).toBe('POST');
	});

	it('resumes and discards a draft', async () => {
		const calls = stubFetch(reply({ id: 'd1', accountId: 'a1', version: 2, source: 'local', to: [], text: 'Hi' }));
		await api.getDraft('d1', 'a1');
		expect(calls[0].url).toBe('/api/v1/drafts/d1?account_id=a1');

		const del = stubFetch({ ok: true, status: 204, json: async () => Promise.reject(new SyntaxError('empty')) });
		await expect(api.discardDraft('d1', 'a1')).resolves.toBeUndefined();
		expect(del[0].url).toBe('/api/v1/drafts/d1?account_id=a1');
		expect(del[0].init.method).toBe('DELETE');
	});

	it('surfaces a stale save as a draft_conflict carrying the newer content', async () => {
		const newer = { id: 'd1', accountId: 'a1', version: 3, source: 'local', to: [], text: 'newer' };
		stubFetch(reply(newer, 409));
		await expect(
			api.saveDraft({ accountId: 'a1', from: 'x@y', to: [], text: 'stale', draftId: 'draft-1', baseVersion: 2 })
		).rejects.toMatchObject({ code: 'draft_conflict', body: newer });
	});

	it('keeps a too-large resume as draft_too_large', async () => {
		stubFetch(reply({ code: 'draft_too_large', message: 'That draft is too large to open here' }, 409));
		await expect(api.getDraft('d1', 'a1')).rejects.toMatchObject({ code: 'draft_too_large' });
	});
});

describe('uploadAttachment', () => {
	afterEach(() => vi.unstubAllGlobals());

	// A buffered copy of a 25 MiB photo doubles its footprint on the phone; the browser
	// can stream a Blob body itself.
	it('hands fetch the Blob itself instead of buffering a copy', async () => {
		let sent: unknown;
		vi.stubGlobal('fetch', async (_url: string, init: RequestInit) => {
			sent = init.body;
			return { ok: true, status: 201, json: async () => ({ id: 'u1', name: 'a.png', mime: 'image/png', size: 3 }) };
		});
		const file = new Blob([new Uint8Array([1, 2, 3])], { type: 'image/png' });
		await api.uploadAttachment('a1', 'a.png', file);
		expect(sent).toBe(file);
	});
});
