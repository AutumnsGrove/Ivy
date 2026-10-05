import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { OutboxItem } from './types.js';

const mocks = vi.hoisted(() => ({ enqueueAction: vi.fn(), listOutbox: vi.fn() }));
vi.mock('./api/client', () => ({ api: { enqueueAction: mocks.enqueueAction, listOutbox: mocks.listOutbox } }));

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
