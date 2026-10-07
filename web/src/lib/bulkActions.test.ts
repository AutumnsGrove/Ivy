import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from './api/errors.js';
import type { OutboxItem } from './types.js';

const mocks = vi.hoisted(() => ({ ask: vi.fn(), enqueueBatch: vi.fn(), remember: vi.fn(), push: vi.fn() }));
vi.mock('./confirm.svelte.js', () => ({ confirm: { ask: mocks.ask } }));
vi.mock('./api/client', () => ({ api: { enqueueBatch: mocks.enqueueBatch } }));
vi.mock('./outbox.svelte.js', () => ({ outbox: { remember: mocks.remember } }));
vi.mock('./toast.js', () => ({ toasts: { push: mocks.push } }));

const { archiveMany, trashMany, flagMany, readMany, tagMany } = await import('./bulkActions.js');

const op = (partial: Partial<OutboxItem>): OutboxItem => ({
	id: 'op',
	accountId: 'a1',
	messageId: 'm1',
	kind: 'move',
	state: 'pending',
	attempts: 0,
	createdAt: '2026-10-07T00:00:00Z',
	updatedAt: '2026-10-07T00:00:00Z',
	...partial
});

beforeEach(() => {
	for (const m of Object.values(mocks)) m.mockReset();
});

describe('moving a selection', () => {
	it('asks once, with the count, and sends nothing until the operator agrees', async () => {
		mocks.ask.mockResolvedValue(false);
		expect(await archiveMany(['a', 'b', 'c'])).toBe(false);
		expect(mocks.ask).toHaveBeenCalledTimes(1);
		expect(mocks.ask.mock.calls[0][0].title).toBe('Archive 3 messages?');
		expect(mocks.enqueueBatch).not.toHaveBeenCalled();
	});

	it('says "1 message" for one, and a delete is the dangerous kind of confirmation', async () => {
		mocks.ask.mockResolvedValue(false);
		await trashMany(['a']);
		expect(mocks.ask.mock.calls[0][0]).toMatchObject({ title: 'Move 1 message to Trash?', tone: 'danger' });
	});

	it('queues one batch, remembers every op for the overlay, and offers one Undo', async () => {
		mocks.ask.mockResolvedValue(true);
		const ops = [op({ id: '1', messageId: 'a', sourceFolderId: 'inbox-1' }), op({ id: '2', messageId: 'b', sourceFolderId: 'inbox-1' })];
		mocks.enqueueBatch.mockResolvedValue({ ops, skipped: [] });

		expect(await archiveMany(['a', 'b'])).toBe(true);
		expect(mocks.enqueueBatch).toHaveBeenCalledExactlyOnceWith({ messageIds: ['a', 'b'], action: 'archive' });
		expect(mocks.remember).toHaveBeenCalledTimes(2);
		expect(mocks.push).toHaveBeenCalledTimes(1);
		expect(mocks.push.mock.calls[0][0]).toMatchObject({ text: 'Archived 2', tone: 'ok', action: { label: 'Undo' } });
	});

	it('Undo is one step: each message goes back to the folder it came from', async () => {
		mocks.ask.mockResolvedValue(true);
		mocks.enqueueBatch.mockResolvedValueOnce({
			ops: [
				op({ id: '1', messageId: 'a', sourceFolderId: 'inbox-1' }),
				op({ id: '2', messageId: 'b', sourceFolderId: 'inbox-1' }),
				op({ id: '3', messageId: 'c', sourceFolderId: 'inbox-2' })
			],
			skipped: []
		});
		await archiveMany(['a', 'b', 'c']);

		mocks.enqueueBatch.mockResolvedValue({ ops: [], skipped: [] });
		mocks.push.mock.calls[0][0].action.run();
		await vi.waitFor(() => expect(mocks.push.mock.calls.at(-1)?.[0]).toMatchObject({ text: 'Undone', tone: 'ok' }));

		const sent = mocks.enqueueBatch.mock.calls.slice(1).map((c) => c[0]);
		expect(sent).toEqual([
			{ messageIds: ['a', 'b'], action: 'move', destinationFolderId: 'inbox-1' },
			{ messageIds: ['c'], action: 'move', destinationFolderId: 'inbox-2' }
		]);
	});

	it('names how many were skipped instead of hiding them behind the success', async () => {
		mocks.ask.mockResolvedValue(true);
		mocks.enqueueBatch.mockResolvedValue({
			ops: [op({ messageId: 'a' })],
			skipped: [
				{ messageId: 'b', code: 'same_folder', message: 'The message is already in that folder' },
				{ messageId: 'c', code: 'not_found', message: 'That message is gone' }
			]
		});
		await archiveMany(['a', 'b', 'c']);
		expect(mocks.push.mock.calls[0][0]).toMatchObject({ text: 'Archived 1', detail: '2 skipped: already there or gone.' });
	});

	it('when every message is skipped it is a failure, not a success', async () => {
		mocks.ask.mockResolvedValue(true);
		mocks.enqueueBatch.mockResolvedValue({
			ops: [],
			skipped: [{ messageId: 'a', code: 'same_folder', message: 'The message is already in that folder' }]
		});
		expect(await archiveMany(['a'])).toBe(false);
		expect(mocks.push.mock.calls[0][0]).toMatchObject({ tone: 'danger' });
	});

	it('shows the server\'s reason when the whole batch is refused, and queues nothing', async () => {
		mocks.ask.mockResolvedValue(true);
		mocks.enqueueBatch.mockRejectedValue(new ApiError('outbox_full', 'There are too many unsent actions; wait for them to finish'));
		expect(await archiveMany(['a', 'b'])).toBe(false);
		expect(mocks.remember).not.toHaveBeenCalled();
		expect(mocks.push.mock.calls[0][0]).toMatchObject({
			tone: 'danger',
			text: 'There are too many unsent actions; wait for them to finish'
		});
	});
});

describe('flags and tags on a selection', () => {
	it('need no confirmation', async () => {
		mocks.enqueueBatch.mockResolvedValue({ ops: [op({ kind: 'flags', messageId: 'a' })], skipped: [] });
		expect(await flagMany(['a'], true)).toBe(true);
		expect(mocks.ask).not.toHaveBeenCalled();
		expect(mocks.enqueueBatch).toHaveBeenCalledWith({ messageIds: ['a'], action: 'flag' });
	});

	it('mark read and unread map to seen and unseen', async () => {
		mocks.enqueueBatch.mockResolvedValue({ ops: [op({ kind: 'flags' })], skipped: [] });
		await readMany(['a', 'b'], true);
		await readMany(['a', 'b'], false);
		expect(mocks.enqueueBatch.mock.calls.map((c) => c[0].action)).toEqual(['seen', 'unseen']);
	});

	it('tagging carries the tag and its Undo is the opposite', async () => {
		mocks.enqueueBatch.mockResolvedValue({ ops: [op({ kind: 'flags', messageId: 'a' })], skipped: [] });
		await tagMany(['a'], { id: 't1', name: 'receipts' }, true);
		expect(mocks.enqueueBatch).toHaveBeenCalledWith({ messageIds: ['a'], action: 'tag', tagId: 't1' });
		expect(mocks.push.mock.calls[0][0].text).toBe('Tagged 1 message “receipts”');

		mocks.push.mock.calls[0][0].action.run();
		await vi.waitFor(() =>
			expect(mocks.enqueueBatch).toHaveBeenLastCalledWith({ messageIds: ['a'], action: 'untag', tagId: 't1' })
		);
	});
});
