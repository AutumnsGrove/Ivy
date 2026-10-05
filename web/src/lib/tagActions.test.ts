import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from './api/errors.js';

const mocks = vi.hoisted(() => ({
	ask: vi.fn(),
	enqueue: vi.fn(),
	push: vi.fn()
}));
vi.mock('./confirm.svelte.js', () => ({ confirm: { ask: mocks.ask } }));
vi.mock('./outbox.svelte.js', () => ({ outbox: { enqueue: mocks.enqueue } }));
vi.mock('./toast.js', () => ({ toasts: { push: mocks.push } }));

const { tagMessage } = await import('./messageActions.js');

const work = { id: 't1', name: 'Work' };

beforeEach(() => {
	mocks.ask.mockReset();
	mocks.enqueue.mockReset();
	mocks.push.mockReset();
});

describe('tagMessage', () => {
	it('queues a tag action without asking: a tag moves and erases nothing', async () => {
		mocks.enqueue.mockResolvedValue({});
		expect(await tagMessage('m1', work, true)).toBe(true);
		expect(mocks.ask).not.toHaveBeenCalled();
		expect(mocks.enqueue).toHaveBeenCalledWith({ messageId: 'm1', action: 'tag', tagId: 't1' });
		expect(mocks.push.mock.calls[0][0]).toMatchObject({ text: 'Tagged “Work”', tone: 'ok' });
	});

	it('queues an untag for removing', async () => {
		mocks.enqueue.mockResolvedValue({});
		await tagMessage('m1', work, false);
		expect(mocks.enqueue).toHaveBeenCalledWith({ messageId: 'm1', action: 'untag', tagId: 't1' });
		expect(mocks.push.mock.calls[0][0].text).toBe('Removed “Work”');
	});

	it('offers Undo, which sends the opposite action for the same tag', async () => {
		mocks.enqueue.mockResolvedValue({});
		await tagMessage('m1', work, true);
		const toast = mocks.push.mock.calls[0][0];
		expect(toast.action.label).toBe('Undo');
		await toast.action.run();
		expect(mocks.enqueue).toHaveBeenLastCalledWith({ messageId: 'm1', action: 'untag', tagId: 't1' });
	});

	it('tells an open picker what an Undo put back, so its switch follows', async () => {
		mocks.enqueue.mockResolvedValue({});
		const restored = vi.fn();
		await tagMessage('m1', work, true, restored);
		await mocks.push.mock.calls[0][0].action.run();
		expect(restored).toHaveBeenCalledWith(false);

		mocks.push.mockReset();
		await tagMessage('m1', work, false, restored);
		await mocks.push.mock.calls[0][0].action.run();
		expect(restored).toHaveBeenLastCalledWith(true);
	});

	it('does not claim an Undo that the server refused', async () => {
		mocks.enqueue.mockResolvedValueOnce({}).mockRejectedValueOnce(new ApiError('unknown_tag', 'That tag no longer exists'));
		const restored = vi.fn();
		await tagMessage('m1', work, true, restored);
		await mocks.push.mock.calls[0][0].action.run();
		expect(restored).not.toHaveBeenCalled();
		expect(mocks.push.mock.calls.at(-1)?.[0]).toMatchObject({ tone: 'danger' });
	});

	it('says why when the server refuses, and reports the tag as not applied', async () => {
		mocks.enqueue.mockRejectedValue(new ApiError('unknown_tag', 'That tag no longer exists'));
		expect(await tagMessage('m1', work, true)).toBe(false);
		expect(mocks.push.mock.calls[0][0]).toMatchObject({ text: 'That tag no longer exists', tone: 'danger' });
	});
});
