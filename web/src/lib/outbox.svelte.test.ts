import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { OutboxItem } from './types.js';

const mocks = vi.hoisted(() => ({ enqueueAction: vi.fn(), listOutbox: vi.fn(), push: vi.fn() }));
vi.mock('./api/client', () => ({ api: { enqueueAction: mocks.enqueueAction, listOutbox: mocks.listOutbox } }));
vi.mock('./toast.js', () => ({ toasts: { push: mocks.push } }));

const { outbox } = await import('./outbox.svelte.js');

function op(partial: Partial<OutboxItem>): OutboxItem {
	return {
		id: 'op-1',
		accountId: 'a1',
		messageId: 'm1',
		kind: 'move',
		state: 'pending',
		attempts: 0,
		createdAt: '2026-10-04T00:00:00Z',
		updatedAt: '2026-10-04T00:00:00Z',
		...partial
	};
}

beforeEach(() => {
	outbox.clear();
	mocks.enqueueAction.mockReset();
	mocks.listOutbox.mockReset();
	mocks.push.mockReset();
});

describe('a live op that fails', () => {
	it('says so, once, instead of the message quietly reappearing (issue #10)', async () => {
		outbox.remember(op({ kind: 'move', state: 'pending' }));
		const failed = op({
			kind: 'move',
			state: 'failed',
			lastErrorCode: 'message_gone',
			lastErrorDetail: 'the message is no longer on the server: a Message-ID search of the folder found 0 messages'
		});
		mocks.listOutbox.mockResolvedValue({ active: [], recent: [failed] });

		await outbox.refresh();
		expect(outbox.hidden('m1')).toBe(false);
		expect(mocks.push).toHaveBeenCalledTimes(1);
		expect(mocks.push.mock.calls[0][0]).toMatchObject({ tone: 'danger', text: "Couldn't move it" });

		// The same terminal op seen again by the next hub-driven refresh stays quiet.
		await outbox.refresh();
		expect(mocks.push).toHaveBeenCalledTimes(1);
	});

	it('stays quiet for an op that finished or was never live here', async () => {
		outbox.remember(op({ id: 'a', kind: 'move', state: 'pending' }));
		mocks.listOutbox.mockResolvedValue({
			active: [],
			recent: [op({ id: 'a', state: 'done' }), op({ id: 'b', state: 'failed', lastErrorCode: 'message_gone' })]
		});
		await outbox.refresh();
		expect(mocks.push).not.toHaveBeenCalled();
	});
});

describe('outbox overlay', () => {
	it('hides a message while its move op is live, and shows it once done', async () => {
		mocks.enqueueAction.mockResolvedValue(op({ kind: 'move', state: 'pending' }));
		await outbox.enqueue({ messageId: 'm1', action: 'archive' });
		expect(outbox.hidden('m1')).toBe(true);

		outbox.remember(op({ kind: 'move', state: 'done' }));
		expect(outbox.hidden('m1')).toBe(false);
	});

	it('keeps a message visible for a flag op and reports the new value', async () => {
		mocks.enqueueAction.mockResolvedValue(op({ kind: 'flags', state: 'pending', flagsAdd: ['\\seen'] }));
		await outbox.enqueue({ messageId: 'm1', action: 'seen' });
		expect(outbox.hidden('m1')).toBe(false);
		expect(outbox.flags('m1')).toEqual({ seen: true });
		expect(outbox.flags('other')).toBeNull();
	});

	it('canonicalises the clear direction too', async () => {
		outbox.remember(op({ kind: 'flags', state: 'pending', flagsClear: ['\\flagged'] }));
		expect(outbox.flags('m1')).toEqual({ flagged: false });
	});

	it('reports the star while a tag keyword op is also live on the same message', () => {
		// A tag is a flags op for a `$ivy-` keyword. It says nothing about the
		// star or the seen state, so it must not mask a live flag op queued after it.
		outbox.remember(op({ id: 'tag', kind: 'flags', state: 'pending', flagsAdd: ['$ivy-work'] }));
		outbox.remember(op({ id: 'star', kind: 'flags', state: 'pending', flagsAdd: ['\\flagged'] }));
		expect(outbox.flags('m1')).toEqual({ flagged: true });
	});

	it('reports which tags a message has live ops for, by slug, later ops winning', () => {
		outbox.remember(op({ id: 'a', kind: 'flags', state: 'pending', flagsAdd: ['$ivy-work', '\\seen'] }));
		outbox.remember(op({ id: 'b', kind: 'flags', state: 'pending', flagsClear: ['$IVY-Home'] }));
		outbox.remember(op({ id: 'c', kind: 'flags', state: 'pending', flagsClear: ['$ivy-work'] }));
		outbox.remember(op({ id: 'd', messageId: 'other', kind: 'flags', state: 'pending', flagsAdd: ['$ivy-x'] }));
		expect(outbox.tags('m1')).toEqual({ work: false, home: false });
		expect(outbox.tags('nothing')).toEqual({});
	});

	it('replaces the overlay from the server active list on refresh', async () => {
		outbox.remember(op({ id: 'stale', kind: 'move', state: 'pending' }));
		mocks.listOutbox.mockResolvedValue({ active: [op({ id: 'fresh', kind: 'flags', state: 'in_flight', flagsAdd: ['\\flagged'] })], recent: [] });
		await outbox.refresh('a1');
		expect(outbox.hidden('m1')).toBe(false);
		expect(outbox.flags('m1')).toEqual({ flagged: true });
	});
});

describe('outbox overlay for an erasure', () => {
	it('hides a message while its expunge is live', () => {
		outbox.remember(op({ kind: 'expunge', state: 'pending' }));
		expect(outbox.hidden('m1')).toBe(true);
	});
});
